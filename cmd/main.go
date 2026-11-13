package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ryu/tools-camp/cli/mdview/internal/mdview"
	"github.com/ryu/tools-camp/cli/mdview/internal/mdview/logging"
)

const (
	usage = `Usage: mdview [-port N] [file]
       mdview --version
       mdview transcribe [-model NAME] [-lang CODE] <recording-dir>

  no args:    browse the current directory in the sidebar.
  file path:  live-watch only that file (Markdown is rendered; source and config files open in the code viewer).
  -port N:    listen port (default 8642; if in use, the following ports are tried in order; 0 picks a random port).
              A fixed port lets the browser remember microphone permission for the localhost:port origin.
  transcribe: transcribe the audio in a recording directory (mdview-recordings/rec-*) with the OpenAI whisper-1 API
              and merge segment timestamps with the event timeline into transcript.json.
              API key: ~/.config/mdview/config.json or the OPENAI_API_KEY environment variable.
              With -model large-v3, the local whisper CLI (openai/whisper-large-v3) is used instead.
              A prompt.txt to paste into an AI is generated whether or not transcription runs.`

	transcribeUsage = "Usage: mdview transcribe [-model NAME] [-lang CODE] <recording-dir>"

	// Default listen port. If the origin changed on every run, the browser could not
	// remember microphone permission, so a fixed port is the default.
	defaultPort = 8642
	// Number of ports to try in order when the default port is in use
	portTries = 20
	// Upper bound for waiting on in-flight HTTP requests (uploads, etc.) at shutdown
	httpShutdownTimeout = 2 * time.Second
)

// Set via -ldflags for releases; shows dev when run directly from source.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// main is the Orchestrator for the whole system.
// It picks the run mode from the CLI args and wires up each Worker/Gateway.
func main() {
	args := os.Args[1:]

	if len(args) >= 1 && args[0] == "transcribe" {
		runTranscribe(args[1:])
		return
	}

	fs := flag.NewFlagSet("mdview", flag.ExitOnError)
	port := fs.Int("port", defaultPort, "listen port (tries the next ports if in use; 0 for a random port)")
	showVersion := fs.Bool("version", false, "print version information")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, usage) }
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	if *showVersion {
		fmt.Printf("mdview %s (commit %s, built %s)\n", version, commit, buildDate)
		return
	}
	rest := fs.Args()

	if len(rest) > 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	if len(rest) == 1 {
		runFileMode(rest[0], *port)
		return
	}

	runFolderMode(*port)
}

// runTranscribe runs the recording transcription subcommand. It still writes prompt.txt when transcription must be skipped.
func runTranscribe(args []string) {
	fs := flag.NewFlagSet("transcribe", flag.ExitOnError)
	model := fs.String("model", "",
		"transcription model (default: saved setting or whisper-1; large-v3 etc. use the local whisper CLI)")
	lang := fs.String("lang", "", "spoken language code (e.g. en). Leave empty to auto-detect")
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, transcribeUsage)
		os.Exit(1)
	}
	if *model == "" {
		selected, err := mdview.ResolveTranscribeModel()
		if err != nil {
			fmt.Fprintln(os.Stderr, "cannot read transcription settings")
			os.Exit(1)
		}
		*model = selected
	}
	dir := fs.Arg(0)

	logging.StartSession()
	logging.LogProcessEvent("start", "start", "transcription started: "+dir, nil)
	skipped := false
	if reason := mdview.TranscribeSkipReason(dir, *model); reason != "" {
		skipped = true
		fmt.Fprintf(os.Stderr, "Skipping transcription (%s) — building the prompt from the interaction log only.\n", reason)
		logging.Warn("[Transcribe] transcription skipped: %s", reason)
	} else if _, err := mdview.TranscribeRecording(dir, *model, *lang); err != nil {
		exitTranscribe("transcription failed", err)
	}
	job := mdview.NewPipeline().RunAfterTranscribe(dir)
	if job.State == mdview.TranscribeStateError {
		exitTranscribe("prompt generation failed", errors.New(job.Error))
	}
	endLabel := "transcription complete: "
	if skipped {
		endLabel = "prompt generated: "
	}
	logging.LogProcessEvent("end", "end", endLabel+filepath.Base(dir), nil)
	logging.EndSession()
	if job.Transcript != "" {
		fmt.Printf("Transcript written: %s\n", job.Transcript)
	}
	fmt.Printf("Prompt written: %s\n", job.Prompt)
}

// exitTranscribe prints the transcription subcommand error once and exits.
func exitTranscribe(label string, err error) {
	logging.Error("[Transcribe] %s: %v", label, err)
	logging.LogProcessEvent("end", "error", label, nil)
	logging.EndSession()
	fmt.Fprintf(os.Stderr, "%s: %v\n", label, err)
	os.Exit(1)
}

// runFileMode runs single-file watch mode.
func runFileMode(filePath string, port int) {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "file not found: %s\n", filePath)
		os.Exit(1)
	}
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "abs path error: %v\n", err)
		os.Exit(1)
	}
	logging.StartSession()
	defer logging.EndSession()

	listener, url := mustListen(port)
	logging.Info("[Main] file mode started: %s (%s)", absPath, url)
	statePath := logging.LogStateSnapshot("startup", map[string]any{
		"mode": "file", "file": absPath, "url": url,
	})
	logging.LogProcessEvent("start", "start", "mdview started: file mode "+filepath.Base(absPath),
		&logging.EventOpts{ArtifactPath: statePath})
	logging.LogProcessEvent("execution", "step_ok", "listening: "+url, nil)

	hub := mdview.NewHub()
	go hub.Run()

	server, err := mdview.NewFileServer(hub, absPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server init error: %v\n", err)
		os.Exit(1)
	}
	go server.Start(listener)
	logging.LogProcessEvent("execution", "step_ok", "HTTP/WS server started", nil)

	// Watcher Worker → Orchestrator: broadcast to the hub on file change.
	// PDFs are binary, so only a change signal is sent and the client re-reads it by URL.
	go mdview.StartFileWatcher(absPath, func(changedAbs string, content []byte) {
		if mdview.IsPDFPath(changedAbs) {
			content = []byte{}
		}
		logging.Info("[Main] broadcasting file change: %s (%d bytes)", changedAbs, len(content))
		hub.Broadcast(content)
	})

	fmt.Printf("Serving %s at %s\n", absPath, url)
	mdview.OpenBrowser(url)
	logging.LogProcessEvent("execution", "step_ok", "browser opened", nil)
	waitForSignal()
	logging.LogProcessEvent("end", "end", "shutdown signal received", nil)
	shutdown(server)
}

// runFolderMode runs current-directory browse mode.
func runFolderMode(port int) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cwd error: %v\n", err)
		os.Exit(1)
	}

	logging.StartSession()
	defer logging.EndSession()

	listener, url := mustListen(port)
	logging.Info("[Main] folder mode started: %s (%s)", cwd, url)
	statePath := logging.LogStateSnapshot("startup", map[string]any{
		"mode": "folder", "root": cwd, "url": url,
	})
	logging.LogProcessEvent("start", "start", "mdview started: folder mode",
		&logging.EventOpts{ArtifactPath: statePath})
	logging.LogProcessEvent("execution", "step_ok", "listening: "+url, nil)

	hub := mdview.NewHub()
	go hub.Run()

	server, err := mdview.NewFolderServer(hub, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server init error: %v\n", err)
		os.Exit(1)
	}
	go server.Start(listener)
	logging.LogProcessEvent("execution", "step_ok", "HTTP/WS server started", nil)

	// Watcher Worker → Orchestrator: watch only the active file and broadcast on change.
	// When the active file changes, the hub callback swaps the watch target (the whole tree is not watched).
	// PDFs are binary, so only a change signal is sent and the client re-reads it by URL.
	watcher := mdview.NewFolderWatcher(func(changedAbs string, content []byte) {
		if mdview.IsPDFPath(changedAbs) {
			content = []byte{}
		}
		msg, err := json.Marshal(mdview.FileMessage{
			Path:    toRelSlash(cwd, changedAbs),
			Content: string(content),
		})
		if err != nil {
			return
		}
		logging.Info("[Main] broadcasting file change: %s (%d bytes)", toRelSlash(cwd, changedAbs), len(content))
		hub.Broadcast(msg)
	})
	if watcher != nil {
		hub.SetOnActiveChange(watcher.SetActive)
		go watcher.Run()
	}

	fmt.Printf("Serving %s at %s\n", cwd, url)
	mdview.OpenBrowser(url)
	logging.LogProcessEvent("execution", "step_ok", "browser opened", nil)
	waitForSignal()
	logging.LogProcessEvent("end", "end", "shutdown signal received", nil)
	shutdown(server)
}

// mustListen opens a listener, trying ports in order starting from the given one.
// If preferred is 0, a random port is used (microphone permission may not be remembered).
func mustListen(preferred int) (net.Listener, string) {
	if preferred == 0 {
		return listenRandom()
	}
	for p := preferred; p < preferred+portTries; p++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			if p != preferred {
				fmt.Printf("Port %d in use → using %d\n", preferred, p)
			}
			return listener, fmt.Sprintf("http://localhost:%d", p)
		}
	}
	fmt.Fprintf(os.Stderr,
		"Warning: ports %d-%d are all in use; falling back to a random port. The origin will change, so the browser may not remember microphone permission.\n",
		preferred, preferred+portTries-1)
	return listenRandom()
}

func listenRandom() (net.Listener, string) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to listen: %v\n", err)
		os.Exit(1)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	return listener, fmt.Sprintf("http://localhost:%d", port)
}

// waitForSignal waits for Ctrl+C (SIGINT)/SIGTERM.
// The first signal starts shutdown; a second one forces an immediate exit
// (so repeated Ctrl+C always gets out if shutdown stalls for any reason).
func waitForSignal() {
	quit := make(chan os.Signal, 2)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	fmt.Printf("\n%s received — shutting down\n", sig)
	go func() {
		<-quit
		fmt.Fprintln(os.Stderr, "forced exit")
		os.Exit(1)
	}()
}

func toRelSlash(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

// shutdown closes the HTTP server first to block new uploads, then waits for in-flight post-processing (transcription, prompt).
// Closing HTTP first ensures no new post-processing starts after WaitPipelines.
// Audio still being recorded lives only in browser memory, so it cannot be saved here.
func shutdown(server *mdview.Server) {
	server.StopHTTP(httpShutdownTimeout)
	if server.PipelinesPending() {
		fmt.Println("Finishing post-processing… (transcription, prompt generation)")
	}
	server.WaitPipelines()
}
