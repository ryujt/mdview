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

	"github.com/ryujt/mdview/internal/mdview"
	"github.com/ryujt/mdview/internal/mdview/logging"
)

const (
	usage = `Usage: mdview [-port N] [file]
       mdview transcribe [-model NAME] [-lang CODE] <recording-dir>

  인자 없음:  현재 디렉토리를 사이드바로 탐색한다.
  파일 경로:  해당 파일만 실시간 감시한다 (마크다운은 렌더링, 소스코드·설정 파일은 코드뷰어).
  -port N:    리슨 포트 (기본 8642, 사용 중이면 이후 포트를 순차 시도, 0이면 임의 포트).
              포트가 고정되어야 브라우저가 localhost:포트 origin의 마이크 권한을 기억한다.
  transcribe: 녹화 디렉토리(mdview-recordings/rec-*)의 오디오를 OpenAI whisper-1 API로 전사하고
              세그먼트 타임스탬프와 이벤트 타임라인을 병합해 transcript.json을 생성한다.
              API 키: ~/.config/mdview/config.json 또는 OPENAI_API_KEY 환경변수.
              -model large-v3 를 주면 로컬 whisper CLI(openai/whisper-large-v3)로 실행한다.
              전사 여부와 무관하게 AI에게 붙여 넣을 prompt.txt를 생성한다.`

	transcribeUsage = "Usage: mdview transcribe [-model NAME] [-lang CODE] <recording-dir>"

	// 기본 리슨 포트. 매 실행 origin이 바뀌면 브라우저가 마이크 권한을 기억하지
	// 못하므로 고정 포트를 기본값으로 사용한다.
	defaultPort = 8642
	// 기본 포트 사용 중일 때 순차적으로 시도할 포트 수
	portTries = 20
	// 종료 시 진행 중인 HTTP 요청(업로드 등)을 기다리는 상한
	httpShutdownTimeout = 2 * time.Second
)

// main은 시스템 전체의 Orchestrator이다.
// CLI 인자에 따라 실행 모드를 결정하고, 각 Worker/Gateway를 와이어링한다.
func main() {
	args := os.Args[1:]

	if len(args) >= 1 && args[0] == "transcribe" {
		runTranscribe(args[1:])
		return
	}

	fs := flag.NewFlagSet("mdview", flag.ExitOnError)
	port := fs.Int("port", defaultPort, "리슨 포트 (사용 중이면 순차 시도, 0이면 임의 포트)")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, usage) }
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
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

// runTranscribe는 녹화 전사 서브커맨드를 실행한다. 전사를 생략해야 하는 경우에도 prompt.txt는 만든다.
func runTranscribe(args []string) {
	fs := flag.NewFlagSet("transcribe", flag.ExitOnError)
	model := fs.String("model", mdview.DefaultTranscribeModel,
		"전사 모델 (whisper-1: OpenAI API, 그 외(large-v3 등): 로컬 whisper CLI)")
	lang := fs.String("lang", "", "발화 언어 코드 (예: ko). 비우면 자동 감지")
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, transcribeUsage)
		os.Exit(1)
	}
	dir := fs.Arg(0)

	logging.StartSession()
	logging.LogProcessEvent("start", "start", "전사 시작: "+dir, nil)
	skipped := false
	if reason := mdview.TranscribeSkipReason(dir, *model); reason != "" {
		skipped = true
		fmt.Fprintf(os.Stderr, "전사를 생략합니다 (%s) — 조작 기록만으로 프롬프트를 만듭니다.\n", reason)
		logging.Warn("[Transcribe] 전사 생략: %s", reason)
	} else if _, err := mdview.TranscribeRecording(dir, *model, *lang); err != nil {
		exitTranscribe("전사 실패", err)
	}
	job := mdview.NewPipeline().RunAfterTranscribe(dir)
	if job.State == mdview.TranscribeStateError {
		exitTranscribe("프롬프트 생성 실패", errors.New(job.Error))
	}
	endLabel := "전사 완료: "
	if skipped {
		endLabel = "프롬프트 생성 완료: "
	}
	logging.LogProcessEvent("end", "end", endLabel+filepath.Base(dir), nil)
	logging.EndSession()
	if job.Transcript != "" {
		fmt.Printf("전사 문서 생성: %s\n", job.Transcript)
	}
	fmt.Printf("프롬프트 생성: %s\n", job.Prompt)
}

// exitTranscribe는 전사 서브커맨드를 에러 한 번만 출력하고 종료한다.
func exitTranscribe(label string, err error) {
	logging.Error("[Transcribe] %s: %v", label, err)
	logging.LogProcessEvent("end", "error", label, nil)
	logging.EndSession()
	fmt.Fprintf(os.Stderr, "%s: %v\n", label, err)
	os.Exit(1)
}

// runFileMode는 단일 파일 감시 모드를 실행한다.
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
	// 녹화 저장 기준: mdview를 실행한 터미널의 작업 디렉토리 (실패 시 파일 디렉토리 폴백)
	workDir, err := os.Getwd()
	if err != nil {
		workDir = ""
	}

	logging.StartSession()
	defer logging.EndSession()

	listener, url := mustListen(port)
	logging.Info("[Main] file 모드 시작: %s (%s)", absPath, url)
	statePath := logging.LogStateSnapshot("startup", map[string]any{
		"mode": "file", "file": absPath, "url": url,
	})
	logging.LogProcessEvent("start", "start", "mdview 시작: file 모드 "+filepath.Base(absPath),
		&logging.EventOpts{ArtifactPath: statePath})
	logging.LogProcessEvent("execution", "step_ok", "리슨: "+url, nil)

	hub := mdview.NewHub()
	go hub.Run()

	server, err := mdview.NewFileServer(hub, absPath, workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server init error: %v\n", err)
		os.Exit(1)
	}
	go server.Start(listener)
	logging.LogProcessEvent("execution", "step_ok", "HTTP/WS 서버 기동", nil)

	// Watcher Worker → Orchestrator: 파일 변경 시 hub로 브로드캐스트.
	// PDF는 바이너리라 내용 없이 변경 신호만 보내고 클라이언트가 URL로 다시 읽는다.
	go mdview.StartFileWatcher(absPath, func(changedAbs string, content []byte) {
		if mdview.IsPDFPath(changedAbs) {
			content = []byte{}
		}
		logging.Info("[Main] 파일 변경 브로드캐스트: %s (%d bytes)", changedAbs, len(content))
		hub.Broadcast(content)
	})

	fmt.Printf("Serving %s at %s\n", absPath, url)
	mdview.OpenBrowser(url)
	logging.LogProcessEvent("execution", "step_ok", "브라우저 오픈", nil)
	waitForSignal()
	logging.LogProcessEvent("end", "end", "종료 시그널 수신", nil)
	shutdown(server)
}

// runFolderMode는 현재 디렉토리 탐색 모드를 실행한다.
func runFolderMode(port int) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cwd error: %v\n", err)
		os.Exit(1)
	}

	logging.StartSession()
	defer logging.EndSession()

	listener, url := mustListen(port)
	logging.Info("[Main] folder 모드 시작: %s (%s)", cwd, url)
	statePath := logging.LogStateSnapshot("startup", map[string]any{
		"mode": "folder", "root": cwd, "url": url,
	})
	logging.LogProcessEvent("start", "start", "mdview 시작: folder 모드",
		&logging.EventOpts{ArtifactPath: statePath})
	logging.LogProcessEvent("execution", "step_ok", "리슨: "+url, nil)

	hub := mdview.NewHub()
	go hub.Run()

	server, err := mdview.NewFolderServer(hub, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server init error: %v\n", err)
		os.Exit(1)
	}
	go server.Start(listener)
	logging.LogProcessEvent("execution", "step_ok", "HTTP/WS 서버 기동", nil)

	// Watcher Worker → Orchestrator: 활성 파일 하나만 감시하고 변경 시 브로드캐스트.
	// 활성 파일이 바뀌면 hub 콜백으로 감시 대상을 교체한다(트리 전체를 감시하지 않음).
	// PDF는 바이너리라 내용 없이 변경 신호만 보내고 클라이언트가 URL로 다시 읽는다.
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
		logging.Info("[Main] 파일 변경 브로드캐스트: %s (%d bytes)", toRelSlash(cwd, changedAbs), len(content))
		hub.Broadcast(msg)
	})
	if watcher != nil {
		hub.SetOnActiveChange(watcher.SetActive)
		go watcher.Run()
	}

	fmt.Printf("Serving %s at %s\n", cwd, url)
	mdview.OpenBrowser(url)
	logging.LogProcessEvent("execution", "step_ok", "브라우저 오픈", nil)
	waitForSignal()
	logging.LogProcessEvent("end", "end", "종료 시그널 수신", nil)
	shutdown(server)
}

// mustListen은 지정 포트부터 순차 시도해 리스너를 연다.
// preferred가 0이면 임의 포트를 사용한다 (마이크 권한이 기억되지 않을 수 있음).
func mustListen(preferred int) (net.Listener, string) {
	if preferred == 0 {
		return listenRandom()
	}
	for p := preferred; p < preferred+portTries; p++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			if p != preferred {
				fmt.Printf("포트 %d 사용 중 → %d 사용\n", preferred, p)
			}
			return listener, fmt.Sprintf("http://localhost:%d", p)
		}
	}
	fmt.Fprintf(os.Stderr,
		"경고: 포트 %d~%d 모두 사용 중이라 임의 포트로 실행합니다. origin이 바뀌어 브라우저가 마이크 권한을 기억하지 못할 수 있습니다.\n",
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

// waitForSignal은 Ctrl+C(SIGINT)/SIGTERM을 기다린다.
// 첫 시그널에 종료 절차를 시작하고, 두 번째 시그널은 즉시 강제 종료한다
// (종료 절차가 어떤 이유로든 지연될 때 Ctrl+C 연타로 확실히 빠져나올 수 있게).
func waitForSignal() {
	quit := make(chan os.Signal, 2)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	fmt.Printf("\n%s 수신 — 종료합니다\n", sig)
	go func() {
		<-quit
		fmt.Fprintln(os.Stderr, "강제 종료")
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

// shutdown은 HTTP 서버를 먼저 닫아 새 업로드·열기 요청을 막고, 전용 Chrome을 닫은 뒤
// 진행 중인 후처리(전사·프롬프트)를 기다린다. HTTP를 먼저 닫아야 정리 도중 Chrome이 다시 기동되거나
// WaitPipelines 이후에 새 후처리가 시작되지 않는다.
// 녹음 중이던 오디오는 브라우저 메모리에만 있으므로 여기서 구할 수 없다.
func shutdown(server *mdview.Server) {
	server.StopHTTP(httpShutdownTimeout)
	server.CloseWeb()
	if server.PipelinesPending() {
		fmt.Println("후처리를 마무리하는 중입니다… (전사·프롬프트 생성)")
	}
	server.WaitPipelines()
}
