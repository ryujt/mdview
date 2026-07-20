package mdview

import (
	"embed"
	"encoding/json"
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

	"github.com/ryujt/mdview/internal/mdview/logging"
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
	wsPath                 = "/ws"
	staticPrefix           = "/static/"
	rootPath               = "/"
	indexFilePath          = "static/index.html"

	// 녹화 업로드 정책: 요청 전체 크기 상한 및 multipart 메모리 상한
	maxRecordingBytes   = 2 << 30  // 2 GiB
	recordingFormMemory = 32 << 20 // 32 MiB (초과분은 임시 파일로 스풀)
	recordingAudioField = "audio"
	recordingMetaField  = "meta"

	originHeader        = "Origin"
	secFetchDestHeader  = "Sec-Fetch-Dest"
	secFetchDestDocument = "document"
	acceptHeader        = "Accept"
	acceptHTML          = "text/html"
)

var localHosts = map[string]bool{
	"localhost": true,
	"127.0.0.1": true,
	"::1":       true,
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  wsReadBufferSize,
	WriteBufferSize: wsWriteBufferSize,
	CheckOrigin:     func(r *http.Request) bool { return isLocalOrigin(r) },
}

// isLocalOrigin은 WS 핸드셰이크의 Origin이 로컬 페이지인지 검증한다.
// 뷰어가 소스코드·설정 파일까지 브로드캐스트하므로 크로스오리진 수신을 차단한다.
func isLocalOrigin(r *http.Request) bool {
	origin := r.Header.Get(originHeader)
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return localHosts[parsed.Hostname()]
}

// Server는 HTTP + WS를 제공하는 Gateway이다.
// file 모드 / folder 모드를 모두 지원한다.
type Server struct {
	mode Mode
	hub  *Hub

	// file 모드: 고정된 파일 절대 경로
	filePath string

	// file 모드: mdview를 실행한 터미널의 작업 디렉토리 (녹화 저장 기준).
	// 비어 있으면 대상 파일의 디렉토리로 폴백한다.
	workDir string

	// folder 모드: 루트 디렉토리 절대 경로
	root string

	// 녹화 자동 전사 작업 상태 (id = 녹화 디렉토리 이름)
	jobMu          sync.Mutex
	transcribeJobs map[string]*TranscribeJob
}

// NewFileServer는 단일 파일을 서빙하는 Server를 생성한다.
// workDir는 mdview를 실행한 터미널의 작업 디렉토리로, 녹화 저장 기준이 된다.
// 비어 있으면 대상 파일의 디렉토리를 기준으로 사용한다.
func NewFileServer(hub *Hub, filePath, workDir string) (*Server, error) {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return nil, err
	}
	if workDir != "" {
		if workDir, err = filepath.Abs(workDir); err != nil {
			return nil, err
		}
	}
	return &Server{mode: ModeFile, hub: hub, filePath: abs, workDir: workDir, transcribeJobs: map[string]*TranscribeJob{}}, nil
}

// NewFolderServer는 디렉토리를 서빙하는 Server를 생성한다.
func NewFolderServer(hub *Hub, root string) (*Server, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Server{mode: ModeFolder, hub: hub, root: abs, transcribeJobs: map[string]*TranscribeJob{}}, nil
}

// Handler는 전체 라우팅이 구성된 HTTP 핸들러를 반환한다 (테스트에서도 사용).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(rootPath, s.handleIndex)
	mux.Handle(staticPrefix, http.FileServer(http.FS(staticFiles)))
	mux.HandleFunc(wsPath, s.handleWS)
	mux.HandleFunc(apiModePath, s.handleMode)
	mux.HandleFunc(apiTreePath, s.handleTree)
	mux.HandleFunc(apiFilePath, s.handleFile)
	mux.HandleFunc(apiSelectPath, s.handleSelect)
	mux.HandleFunc(apiRecordingPath, s.handleRecording)
	mux.HandleFunc(apiRecordingStatusPath, s.handleRecordingStatus)
	return mux
}

// Start는 주어진 리스너에서 HTTP 서버를 실행한다 (블로킹).
func (s *Server) Start(listener net.Listener) {
	server := &http.Server{Handler: s.Handler()}
	if err := server.Serve(listener); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// 루트 및 뷰어 대상 파일 경로는 모두 index.html을 서빙한다.
	// 이를 통해 "/docs/guide.md" 같은 deep-link 로 새로고침해도 SPA가 해당 파일을 열 수 있다.
	// (/static/ 과 /api/ 경로는 별도 핸들러가 먼저 처리하므로 여기로 오지 않는다.)
	if r.URL.Path != rootPath && !s.isDeepLinkRequest(r) {
		// 마크다운 안에서 참조되는 이미지/기타 에셋을 디스크에서 직접 서빙한다.
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

// isDeepLinkRequest는 SPA deep-link 로 취급할 요청인지 반환한다.
// .md는 두 모드 공통이다. 그 외 뷰어 대상 파일은 folder 모드의 최상위 문서 탐색
// 요청일 때만 deep-link 로 취급하여, 마크다운 인라인 HTML이 참조하는 에셋이나
// curl 직접 요청은 기존처럼 원본 파일로 서빙되게 한다.
func (s *Server) isDeepLinkRequest(r *http.Request) bool {
	if IsMarkdownPath(r.URL.Path) {
		return true
	}
	if s.mode != ModeFolder || !IsViewablePath(r.URL.Path) {
		return false
	}
	return isDocumentRequest(r)
}

// isDocumentRequest는 브라우저의 최상위 문서 탐색 요청인지 판별한다.
// Sec-Fetch-Dest 미지원 클라이언트는 Accept 헤더로 근사 판별한다.
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

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	if s.mode != ModeFolder {
		http.Error(w, "tree is only available in folder mode", http.StatusBadRequest)
		return
	}
	tree, err := BuildTree(s.root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, tree)
}

// handleFile은 folder 모드에서 상대 경로의 파일을 읽어 반환한다.
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

// handleSelect는 folder 모드에서 현재 뷰의 활성 파일을 서버 측에 알려준다.
// 이후 파일 변경 감시 결과는 이 파일에 한해 WS로 브로드캐스트된다.
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

// handleRecording은 브라우저가 녹화 종료 시 업로드하는 오디오+이벤트 메타를 저장한다.
// 저장 위치는 folder 모드면 루트, file 모드면 대상 파일의 디렉토리이다.
func (s *Server) handleRecording(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// 크로스오리진 페이지의 임의 업로드를 차단한다 (WS Origin 검증과 동일 정책).
	if !isLocalOrigin(r) {
		logging.Warn("[Recording] 크로스오리진 업로드 거부: %s", r.Header.Get(originHeader))
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRecordingBytes)
	if err := r.ParseMultipartForm(recordingFormMemory); err != nil {
		logging.Warn("[Recording] multipart 파싱 실패: %v", err)
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

	dir, audioName, err := SaveRecording(s.recordingBaseDir(), audio, header.Header.Get("Content-Type"), meta, time.Now())
	eventCount, eventsSummary := summarizeRecordingEvents(meta)
	if err != nil {
		logging.Error("[Recording] 저장 실패: %v", err)
		logging.LogRecordingUpload(string(s.mode), "-", header.Filename, header.Size,
			eventCount, eventsSummary, err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	logging.Info("[Recording] 저장: %s (%s %d bytes, 이벤트 %d개)", dir, audioName, header.Size, eventCount)
	logging.LogRecordingUpload(string(s.mode), dir, audioName, header.Size, eventCount, eventsSummary, "")

	// API 키가 있으면 백그라운드에서 자동 전사한다.
	transcribing := s.startAutoTranscribe(dir)

	writeJSON(w, http.StatusOK, RecordingResult{
		Dir:          dir,
		Audio:        audioName,
		Events:       eventsFileName,
		ID:           filepath.Base(dir),
		Transcribing: transcribing,
	})
}

// startAutoTranscribe는 조건이 맞으면 백그라운드 전사를 시작하고 시작 여부를 반환한다.
// 조건: OpenAI API 키가 있고, 오디오가 API 업로드 상한(25MB) 이하일 때.
func (s *Server) startAutoTranscribe(dir string) bool {
	if _, err := ResolveOpenAIKey(); err != nil {
		logging.Info("[Transcribe] API 키 없음 — 자동 전사 생략 (mdview transcribe 수동 실행 가능)")
		return false
	}
	audioPath, err := findRecordingAudio(dir)
	if err != nil {
		return false
	}
	if size := fileSize(audioPath); size > maxAPIAudioBytes {
		logging.Warn("[Transcribe] 오디오 %dMB — API 상한(25MB) 초과로 자동 전사 생략", size>>20)
		fmt.Fprintf(os.Stderr, "오디오가 25MB를 초과해 자동 전사를 생략합니다: %s\n", dir)
		return false
	}

	id := filepath.Base(dir)
	s.setJob(&TranscribeJob{ID: id, State: TranscribeStateRunning, Dir: dir})
	logging.Info("[Transcribe] 자동 전사 시작: %s", id)

	go func() {
		out, err := TranscribeRecording(dir, DefaultTranscribeModel, "")
		if err != nil {
			logging.Error("[Transcribe] 자동 전사 실패 (%s): %v", id, err)
			fmt.Fprintf(os.Stderr, "자동 전사 실패 (%s): %v\n", id, err)
			s.setJob(&TranscribeJob{ID: id, State: TranscribeStateError, Dir: dir, Error: err.Error()})
			return
		}
		job := &TranscribeJob{ID: id, State: TranscribeStateDone, Dir: dir, Transcript: out}
		if s.mode == ModeFolder {
			if rel, err := filepath.Rel(s.root, out); err == nil && !strings.HasPrefix(rel, "..") {
				job.TranscriptRel = filepath.ToSlash(rel)
			}
		}
		s.setJob(job)
		logging.Info("[Transcribe] 자동 전사 완료: %s → %s", id, out)
		fmt.Printf("자동 전사 완료: %s\n", out)
	}()
	return true
}

func (s *Server) setJob(job *TranscribeJob) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	job.UpdatedAt = time.Now().Format(time.RFC3339)
	s.transcribeJobs[job.ID] = job
}

// handleRecordingStatus는 자동 전사 작업 상태를 반환한다.
// ?id=<rec-...> 로 단건 조회, 없으면 전체 목록을 반환한다.
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

// recordingBaseDir는 녹화 저장의 기준 디렉토리를 반환한다.
// 두 모드 모두 mdview를 실행한 터미널의 작업 디렉토리가 기준이다
// (folder 모드의 root가 곧 실행 시 작업 디렉토리).
func (s *Server) recordingBaseDir() string {
	if s.mode == ModeFile {
		if s.workDir != "" {
			return s.workDir
		}
		return filepath.Dir(s.filePath)
	}
	return s.root
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &Client{conn: conn, send: make(chan []byte, clientSendBuffer)}
	s.hub.Register(c)

	// file 모드는 연결 즉시 초기 컨텐츠를 push한다.
	if s.mode == ModeFile {
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

// serveAsset은 마크다운에서 참조하는 이미지/일반 파일을 디스크에서 서빙한다.
// folder 모드는 root 기준, file 모드는 파일이 위치한 디렉토리 기준이다.
// 보안: 경로가 루트(또는 base dir)를 벗어나면 거부한다. .md는 SPA 라우팅을 위해 처리하지 않는다.
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
	http.ServeFile(w, r, candidate)
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
