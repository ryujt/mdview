package mdview

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ryu/tools-camp/cli/mdview/internal/mdview/logging"
)

//go:embed static
var staticFiles embed.FS

const (
	wsReadBufferSize  = 1024
	wsWriteBufferSize = 1024

	apiModePath            = "/api/mode"
	apiTreePath            = "/api/tree"
	apiFilePath            = "/api/file"
	apiSelectPath          = "/api/select"
	apiRecordingPath       = "/api/recording"
	apiRecordingStatusPath = "/api/recording/status"
	apiRecordingFilePath   = "/api/recording/file"
	wsPath                 = "/ws"
	staticPrefix           = "/static/"
	rootPath               = "/"
	indexFilePath          = "static/index.html"

	// Recording upload policy: total request size limit and multipart memory limit
	maxRecordingBytes   = 2 << 30  // 2 GiB
	recordingFormMemory = 32 << 20 // 32 MiB (the excess spills to temp files)
	recordingAudioField = "audio"
	recordingMetaField  = "meta"

	originHeader         = "Origin"
	secFetchDestHeader   = "Sec-Fetch-Dest"
	secFetchDestDocument = "document"
	acceptHeader         = "Accept"
	acceptHTML           = "text/html"

	treeInvalidPathMessage = "invalid path"
	treeListFailedMessage  = "failed to list directory"

	secFetchSiteHeader = "Sec-Fetch-Site"
	secFetchSiteSame   = "same-origin"

	methodNotAllowedMessage = "method not allowed"
	forbiddenOriginMessage  = "forbidden origin"
	forbiddenHostMessage    = "forbidden host"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  wsReadBufferSize,
	WriteBufferSize: wsWriteBufferSize,
	CheckOrigin:     func(r *http.Request) bool { return isSameOrigin(r) },
}

// localHostnames are the host names mdview answers to (the server binds only to 127.0.0.1).
var localHostnames = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}

// isLocalHostname reports whether a host name (without port) is a local loopback name.
func isLocalHostname(hostname string) bool {
	return localHostnames[strings.ToLower(hostname)]
}

// isSameOrigin requires both conditions when an Origin header is present:
//  1. The Origin host name is local (localhost, 127.0.0.1, ::1) — a DNS rebinding page (http://evil.com:8642)
//     can match the Host header too, so comparing host:port alone cannot stop it.
//  2. The Origin host:port equals the request target (r.Host) — pages on other localhost ports in the same
//     browser (dev servers, etc.) cannot be stopped by host name alone.
//
// Requests without Origin (curl, some same-document GETs) pass (requireLocalHost validates Host itself).
// WS (the viewer broadcasts even source and config files), recording uploads, and the settings API all use this check.
func isSameOrigin(r *http.Request) bool {
	origin := r.Header.Get(originHeader)
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return isLocalHostname(parsed.Hostname()) && strings.EqualFold(parsed.Host, r.Host)
}

// requestHostname returns r.Host without the port (unchanged if there is no port).
func requestHostname(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		return strings.Trim(r.Host, "[]")
	}
	return host
}

// requireLocalHost is a server-wide guard requiring every request's Host to be a local host name.
// If DNS rebinding makes evil.com resolve to 127.0.0.1, GETs and WS without Origin also reach our server,
// so this also blocks existing GET endpoints without an Origin check (/api/file, etc.).
func requireLocalHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalHostname(requestHostname(r)) {
			logging.Warn("[Server] rejected non-local Host: %q path=%s", r.Host, r.URL.Path)
			http.Error(w, forbiddenHostMessage, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hasLocalOrigin is a stricter check for requests that change server state, such as saving settings.
// To block requests from other sites in the same browser, it requires an Origin header that is same-origin (local host and matching host:port), and Sec-Fetch-Site, if present, must be same-origin.
func hasLocalOrigin(r *http.Request) bool {
	if r.Header.Get(originHeader) == "" {
		return false
	}
	if site := r.Header.Get(secFetchSiteHeader); site != "" && site != secFetchSiteSame {
		return false
	}
	return isSameOrigin(r)
}

// Server is the Gateway that serves HTTP + WS.
// It supports both file mode and folder mode.
type Server struct {
	mode Mode
	hub  *Hub

	// file mode: absolute path of the fixed file
	filePath string

	// folder mode: absolute path of the root directory
	root string

	// Auto-transcription job state per recording (id = recording directory name)
	jobMu          sync.Mutex
	transcribeJobs map[string]*TranscribeJob

	// pipeline is the post-processing (transcription, prompt) after a recording is saved.
	pipeline   *Pipeline
	completion *recordingCompletion
	// pending tracks in-flight post-processing goroutines. Shutdown waits on it.
	pending sync.WaitGroup

	// httpServer is the HTTP server created by Start (closed by StopHTTP).
	httpMu     sync.Mutex
	httpServer *http.Server
}

// NewFileServer creates a Server that serves a single file.
func NewFileServer(hub *Hub, filePath string) (*Server, error) {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return nil, err
	}
	s := newServer(ModeFile, hub)
	s.filePath = abs
	return s, nil
}

// NewFolderServer creates a Server that serves a directory.
func NewFolderServer(hub *Hub, root string) (*Server, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	s := newServer(ModeFolder, hub)
	s.root = abs
	return s, nil
}

func newServer(mode Mode, hub *Hub) *Server {
	s := &Server{
		mode: mode, hub: hub,
		transcribeJobs: map[string]*TranscribeJob{},
		pipeline:       NewPipeline(),
		completion:     newRecordingCompletion(),
	}
	return s
}

// Handler returns the HTTP handler with all routes configured (also used in tests).
// Every route is wrapped in requireLocalHost to reject non-local Hosts.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(rootPath, s.handleIndex)
	mux.Handle(staticPrefix, http.FileServer(http.FS(staticFiles)))
	mux.HandleFunc(wsPath, s.handleWS)
	mux.HandleFunc(apiModePath, s.handleMode)
	mux.HandleFunc(apiSettingsPath, s.handleSettings)
	mux.HandleFunc(apiTreePath, s.handleTree)
	mux.HandleFunc(apiFilePath, s.handleFile)
	mux.HandleFunc(apiSelectPath, s.handleSelect)
	mux.HandleFunc(apiRecordingPath, s.handleRecording)
	mux.HandleFunc(apiRecordingStatusPath, s.handleRecordingStatus)
	mux.HandleFunc(apiRecordingFilePath, s.handleRecordingFile)
	return requireLocalHost(mux)
}

// Start runs the HTTP server on the given listener (blocking). It returns quietly when stopped via StopHTTP.
func (s *Server) Start(listener net.Listener) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.pruneRecordings(time.Now())
	go s.runRecordingRetention(ctx, recordingCleanupInterval)
	server := &http.Server{Handler: s.Handler()}
	s.httpMu.Lock()
	s.httpServer = server
	s.httpMu.Unlock()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
	}
}

// StopHTTP closes the HTTP server to new requests and waits up to timeout for in-flight ones.
// Call it on shutdown before draining post-processing so no uploads arrive in between. It is a no-op before Start.
func (s *Server) StopHTTP(timeout time.Duration) {
	s.httpMu.Lock()
	server := s.httpServer
	s.httpMu.Unlock()
	if server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logging.Warn("[Server] HTTP shutdown wait exceeded: %v", err)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// The root and viewable file paths all serve index.html.
	// This lets the SPA open the file even when reloading a deep link like "/docs/guide.md".
	// (/static/ and /api/ paths are handled first by their own handlers and never reach here.)
	if r.URL.Path != rootPath && !s.isDeepLinkRequest(r) {
		// Serve images and other assets referenced from Markdown directly from disk.
		if s.serveAsset(w, r) {
			return
		}
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(staticFiles, indexFilePath)
	if err != nil {
		http.Error(w, "index.html not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// isDeepLinkRequest reports whether a request should be treated as an SPA deep link.
// .md applies in both modes. Other viewable files are treated as deep links only for
// top-level document navigations in folder mode, so assets referenced by inline HTML in Markdown
// and direct curl requests are still served as the original file.
func (s *Server) isDeepLinkRequest(r *http.Request) bool {
	if IsMarkdownPath(r.URL.Path) {
		return true
	}
	if s.mode != ModeFolder || !IsViewablePath(r.URL.Path) {
		return false
	}
	return isDocumentRequest(r)
}

// isDocumentRequest reports whether a request is a top-level browser document navigation.
// For clients without Sec-Fetch-Dest, the Accept header is used as an approximation.
func isDocumentRequest(r *http.Request) bool {
	if dest := r.Header.Get(secFetchDestHeader); dest != "" {
		return dest == secFetchDestDocument
	}
	return strings.Contains(r.Header.Get(acceptHeader), acceptHTML)
}

func (s *Server) handleMode(w http.ResponseWriter, r *http.Request) {
	info := ModeInfo{Mode: s.mode}
	switch s.mode {
	case ModeFile:
		info.Filename = s.filePath
	case ModeFolder:
		info.Root = s.root
	}
	writeJSON(w, http.StatusOK, info)
}

// handleTree returns the one-level listing of the requested directory (?path=, empty for root) in folder mode.
// The sidebar calls it each time a folder is expanded, so the whole tree is not scanned recursively.
// Error responses use generic messages so server paths are not exposed.
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	if s.mode != ModeFolder {
		http.Error(w, "tree is only available in folder mode", http.StatusBadRequest)
		return
	}
	tree, err := BuildTreeLevel(s.root, r.URL.Query().Get("path"))
	if err != nil {
		if errors.Is(err, errTreePath) {
			http.Error(w, treeInvalidPathMessage, http.StatusBadRequest)
			return
		}
		http.Error(w, treeListFailedMessage, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, tree)
}

// handleFile reads and returns a file by relative path in folder mode.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	if s.mode != ModeFolder {
		http.Error(w, "file API is only available in folder mode", http.StatusBadRequest)
		return
	}
	rel := r.URL.Query().Get("path")
	abs, err := ResolveRelPath(s.root, rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(content)
}

// handleSelect tells the server which file is active in the current view (folder mode).
// From then on, file change events are broadcast over WS for this file only.
func (s *Server) handleSelect(w http.ResponseWriter, r *http.Request) {
	if s.mode != ModeFolder {
		http.Error(w, "select API is only available in folder mode", http.StatusBadRequest)
		return
	}
	rel := r.URL.Query().Get("path")
	if rel == "" {
		s.hub.SetActiveFile("")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	abs, err := ResolveRelPath(s.root, rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.hub.SetActiveFile(abs)
	w.WriteHeader(http.StatusNoContent)
}

// handleRecording saves the audio and event metadata the browser uploads when a recording ends.
// It is saved under the root in folder mode, or the target file's directory in file mode.
func (s *Server) handleRecording(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, methodNotAllowedMessage, http.StatusMethodNotAllowed)
		return
	}
	// Block arbitrary uploads from cross-origin pages (same policy as the WS Origin check).
	// Multipart POSTs have no preflight, so pages on other localhost ports must be blocked here too.
	if !isSameOrigin(r) {
		logging.Warn("[Recording] rejected cross-origin upload: %s", r.Header.Get(originHeader))
		http.Error(w, forbiddenOriginMessage, http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRecordingBytes)
	if err := r.ParseMultipartForm(recordingFormMemory); err != nil {
		logging.Warn("[Recording] multipart parse failed: %v", err)
		http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
		return
	}
	audio, header, err := r.FormFile(recordingAudioField)
	if err != nil {
		http.Error(w, "missing audio part: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer audio.Close()

	var meta map[string]any
	if err := json.Unmarshal([]byte(r.FormValue(recordingMetaField)), &meta); err != nil || meta == nil {
		http.Error(w, "meta must be a JSON object", http.StatusBadRequest)
		return
	}

	base, err := recordingBaseDir()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.pruneRecordings(time.Now())
	dir, audioName, err := SaveRecording(base, audio, header.Header.Get("Content-Type"), meta, time.Now())
	eventCount, eventsSummary := summarizeRecordingEvents(meta)
	if err != nil {
		logging.Error("[Recording] save failed: %v", err)
		logging.LogRecordingUpload(string(s.mode), "-", header.Filename, header.Size,
			eventCount, eventsSummary, err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	logging.Info("[Recording] saved: %s (%s %d bytes, %d events)", dir, audioName, header.Size, eventCount)
	warnIfAudioUnusable(filepath.Join(dir, audioName))
	logging.LogRecordingUpload(string(s.mode), dir, audioName, header.Size, eventCount, eventsSummary, "")

	// Start post-processing (transcription if possible → prompt.txt) in the background.
	s.startPipeline(dir)

	writeJSON(w, http.StatusOK, RecordingResult{
		Dir:          dir,
		Audio:        audioName,
		Events:       eventsFileName,
		ID:           filepath.Base(dir),
		Transcribing: true,
	})
}

// startPipeline starts post-processing in the background (the upload response returns immediately).
func (s *Server) startPipeline(dir string) {
	id := filepath.Base(dir)
	s.setJob(&TranscribeJob{ID: id, State: TranscribeStateRunning, Dir: dir})
	logging.Info("[Pipeline] post-processing started: %s", id)
	s.pending.Add(1)
	go func() {
		defer s.pending.Done()
		job := s.pipeline.Run(dir)
		job.TranscriptRel = s.relToRoot(job.Transcript)
		job.PromptRel = s.relToRoot(job.Prompt)
		s.completion.complete(job)
		s.setJob(job)
		if job.State == TranscribeStateError {
			logging.Error("[Pipeline] post-processing failed (%s): %s", id, job.Error)
			fmt.Fprintf(os.Stderr, "Post-processing failed (%s): %s\n", id, job.Error)
			return
		}
		logging.Info("[Pipeline] post-processing complete: %s → %s", id, job.Prompt)
		fmt.Printf("Done — prompt saved: %s\n", job.Prompt)
	}()
}

// relToRoot converts a path under the root to a slash-separated relative path in folder mode (for opening directly in the viewer). Otherwise it returns an empty string.
func (s *Server) relToRoot(abs string) string {
	if s.mode != ModeFolder || abs == "" {
		return ""
	}
	rel, err := filepath.Rel(s.root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}

// WaitPipelines waits until all in-flight post-processing finishes (for shutdown and test cleanup).
func (s *Server) WaitPipelines() {
	s.pending.Wait()
}

// PipelinesPending reports whether any post-processing is still running (for the shutdown notice).
func (s *Server) PipelinesPending() bool {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	for _, job := range s.transcribeJobs {
		if job.State == TranscribeStateRunning {
			return true
		}
	}
	return false
}

func (s *Server) setJob(job *TranscribeJob) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	job.UpdatedAt = time.Now().Format(time.RFC3339)
	s.transcribeJobs[job.ID] = job
}

// handleRecordingStatus returns auto-transcription job status.
// ?id=<rec-...> returns a single job; without it, the full list is returned.
func (s *Server) handleRecordingStatus(w http.ResponseWriter, r *http.Request) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if id := r.URL.Query().Get("id"); id != "" {
		job, ok := s.transcribeJobs[id]
		if !ok {
			http.Error(w, "unknown recording id", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, job)
		return
	}
	jobs := make([]*TranscribeJob, 0, len(s.transcribeJobs))
	for _, j := range s.transcribeJobs {
		jobs = append(jobs, j)
	}
	writeJSON(w, http.StatusOK, jobs)
}

// handleRecordingFile serves only result files created by this process.
// This lets the completion toast open results even when the recording is stored outside the project.
func (s *Server) handleRecordingFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, methodNotAllowedMessage, http.StatusMethodNotAllowed)
		return
	}
	if !isSameOrigin(r) {
		http.Error(w, forbiddenOriginMessage, http.StatusForbidden)
		return
	}
	s.jobMu.Lock()
	job := s.transcribeJobs[r.URL.Query().Get("id")]
	var name, dir string
	if job != nil {
		dir = job.Dir
		switch r.URL.Query().Get("name") {
		case promptFileName:
			if job.Prompt != "" {
				name = promptFileName
			}
		case transcriptFileName:
			if job.Transcript != "" {
				name = transcriptFileName
			}
		}
	}
	s.jobMu.Unlock()
	if name == "" {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	contentType := "text/plain; charset=utf-8"
	if name == transcriptFileName {
		contentType = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &Client{conn: conn, send: make(chan []byte, clientSendBuffer)}
	s.hub.Register(c)

	// In file mode, the initial content is pushed as soon as a client connects.
	// PDFs are binary, so they are not sent as WS text frames (the client reads them by URL).
	if s.mode == ModeFile && !IsPDFPath(s.filePath) {
		if content, err := os.ReadFile(s.filePath); err == nil {
			c.send <- content
		}
	}

	go s.writePump(c)
	go s.readPump(c)
}

func (s *Server) writePump(c *Client) {
	defer c.conn.Close()
	for msg := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

func (s *Server) readPump(c *Client) {
	defer func() {
		s.hub.Unregister(c)
		c.conn.Close()
	}()
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

// serveAsset serves images and other files referenced from Markdown from disk.
// Paths are relative to root in folder mode, or to the file's directory in file mode.
// Security: paths escaping the root (or base dir) are rejected. .md is left to SPA routing.
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request) bool {
	var base string
	switch s.mode {
	case ModeFolder:
		base = s.root
	case ModeFile:
		base = filepath.Dir(s.filePath)
	default:
		return false
	}
	rel := strings.TrimPrefix(r.URL.Path, "/")
	if rel == "" {
		return false
	}
	candidate := filepath.Clean(filepath.Join(base, filepath.FromSlash(rel)))
	baseWithSep := base + string(filepath.Separator)
	if candidate != base && !strings.HasPrefix(candidate, baseWithSep) {
		return false
	}
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return false
	}
	// ServeFile redirects index.html requests to the directory URL.
	// HTML preview must return the original document as-is to avoid a nested viewer.
	if ext := strings.ToLower(filepath.Ext(candidate)); ext == ".html" || ext == ".htm" {
		file, err := os.Open(candidate)
		if err != nil {
			return false
		}
		defer file.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, info.Name(), info.ModTime(), file)
		return true
	}
	http.ServeFile(w, r, candidate)
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
