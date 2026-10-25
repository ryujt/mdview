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
	wsPath                 = "/ws"
	staticPrefix           = "/static/"
	rootPath               = "/"
	indexFilePath          = "static/index.html"

	// 녹화 업로드 정책: 요청 전체 크기 상한 및 multipart 메모리 상한
	maxRecordingBytes   = 2 << 30  // 2 GiB
	recordingFormMemory = 32 << 20 // 32 MiB (초과분은 임시 파일로 스풀)
	recordingAudioField = "audio"
	recordingMetaField  = "meta"

	originHeader         = "Origin"
	secFetchDestHeader   = "Sec-Fetch-Dest"
	secFetchDestDocument = "document"
	acceptHeader         = "Accept"
	acceptHTML           = "text/html"

	treeInvalidPathMessage = "invalid path"
	treeListFailedMessage  = "failed to list directory"

	apiWebStartPath  = "/api/web/start"
	apiWebStopPath   = "/api/web/stop"
	apiWebCancelPath = "/api/web/cancel"
	apiWebLivePath   = "/api/web/live"
	apiWebOpenPath   = "/api/web/open"

	secFetchSiteHeader = "Sec-Fetch-Site"
	secFetchSiteSame   = "same-origin"

	methodNotAllowedMessage = "method not allowed"
	forbiddenOriginMessage  = "forbidden origin"
	forbiddenHostMessage    = "forbidden host"

	// /api/web/* JSON 본문 상한 (start·stop·open 모두 짧은 객체 하나다)
	maxWebJSONBodyBytes = 1 << 10
	// 브라우저가 보낸 녹음 시작 시각이 서버 시계와 이보다 멀면 서버 시각으로 대체한다
	maxStartEpochSkew = time.Hour
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  wsReadBufferSize,
	WriteBufferSize: wsWriteBufferSize,
	CheckOrigin:     func(r *http.Request) bool { return isSameOrigin(r) },
}

// localHostnames는 mdview가 응답하는 호스트 이름이다 (서버는 127.0.0.1에만 바인드한다).
var localHostnames = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}

// isLocalHostname은 포트를 뗀 호스트 이름이 로컬 루프백 이름인지 반환한다.
func isLocalHostname(hostname string) bool {
	return localHostnames[strings.ToLower(hostname)]
}

// isSameOrigin은 Origin 헤더가 있으면 두 조건을 모두 요구한다.
//  1. Origin의 호스트 이름이 로컬(localhost, 127.0.0.1, ::1)이다 — DNS 리바인딩 페이지(http://evil.com:8642)는
//     Host 헤더까지 일치시킬 수 있으므로 호스트:포트 비교만으로는 막을 수 없다.
//  2. Origin의 호스트:포트가 요청 대상(r.Host)과 같다 — 다른 localhost 포트의 페이지는 전용 Chrome 안에서
//     흔히 열리는 대상이므로 호스트 이름만으로는 막을 수 없다.
//
// Origin이 없는 요청(curl, 같은 문서의 일부 GET)은 통과시킨다 (Host 자체는 requireLocalHost가 검증한다).
// WS(뷰어가 소스코드·설정 파일까지 브로드캐스트), 녹화 업로드, 웹 폴링이 모두 이 검증을 쓴다.
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

// requestHostname은 r.Host에서 포트를 뗀 호스트 이름을 반환한다 (포트가 없으면 원문).
func requestHostname(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		return strings.Trim(r.Host, "[]")
	}
	return host
}

// requireLocalHost는 모든 요청의 Host가 로컬 호스트 이름일 것을 요구하는 서버 전역 가드이다.
// DNS 리바인딩으로 evil.com이 127.0.0.1을 가리키게 되면 Origin 없이 오는 GET·WS도 우리 서버에 닿으므로,
// Origin 검증이 없는 기존 GET 엔드포인트(/api/file 등)까지 여기서 막는다.
func requireLocalHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalHostname(requestHostname(r)) {
			logging.Warn("[Server] 로컬이 아닌 Host 거부: %q path=%s", r.Host, r.URL.Path)
			http.Error(w, forbiddenHostMessage, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hasLocalOrigin은 웹 세션 상태를 바꾸는 요청에 쓰는 더 엄격한 검증이다.
// mdview는 적대적일 수 있는 임의 사이트를 같은 머신의 Chrome에 띄우므로
// Origin 헤더가 존재하고 같은 오리진(로컬 호스트 + 호스트:포트 일치)일 것을 요구하며, Sec-Fetch-Site가 있으면 same-origin이어야 한다.
func hasLocalOrigin(r *http.Request) bool {
	if r.Header.Get(originHeader) == "" {
		return false
	}
	if site := r.Header.Get(secFetchSiteHeader); site != "" && site != secFetchSiteSame {
		return false
	}
	return isSameOrigin(r)
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

	// web은 녹음 중 웹 페이지 조작 이벤트 버퍼이다 (WebCapture → WebSession.Add).
	web *WebSession
	// webOpener는 전용 Chrome 수명을 소유한다 (수집 payload는 web.Add로 간다).
	webOpener *WebOpener
	// pipeline은 녹화 저장 후 후처리(전사·프롬프트)이다.
	pipeline *Pipeline
	// pending은 진행 중인 후처리 goroutine이다. 종료 시 여기서 기다린다.
	pending sync.WaitGroup

	// httpServer는 Start가 만든 HTTP 서버이다 (StopHTTP가 종료한다).
	httpMu     sync.Mutex
	httpServer *http.Server
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
	s := newServer(ModeFile, hub)
	s.filePath = abs
	s.workDir = workDir
	return s, nil
}

// NewFolderServer는 디렉토리를 서빙하는 Server를 생성한다.
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
		web:            NewWebSession(),
		pipeline:       NewPipeline(),
	}
	s.webOpener = NewWebOpener(s.web.Add)
	return s
}

// Handler는 전체 라우팅이 구성된 HTTP 핸들러를 반환한다 (테스트에서도 사용).
// 모든 경로는 requireLocalHost로 감싸 로컬이 아닌 Host를 거부한다.
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
	mux.HandleFunc(apiWebStartPath, s.handleWebStart)
	mux.HandleFunc(apiWebStopPath, s.handleWebStop)
	mux.HandleFunc(apiWebCancelPath, s.handleWebCancel)
	mux.HandleFunc(apiWebLivePath, s.handleWebLive)
	mux.HandleFunc(apiWebOpenPath, s.handleWebOpen)
	return requireLocalHost(mux)
}

// Start는 주어진 리스너에서 HTTP 서버를 실행한다 (블로킹). StopHTTP로 종료하면 조용히 반환한다.
func (s *Server) Start(listener net.Listener) {
	server := &http.Server{Handler: s.Handler()}
	s.httpMu.Lock()
	s.httpServer = server
	s.httpMu.Unlock()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
	}
}

// StopHTTP는 새 요청을 받지 않도록 HTTP 서버를 닫고, 진행 중인 요청을 timeout까지 기다린다.
// 종료 경로에서 Chrome·후처리 정리 전에 호출해 그 사이 업로드·열기 요청이 들어오지 않게 한다. Start 전이면 아무것도 하지 않는다.
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
		logging.Warn("[Server] HTTP 종료 대기 초과: %v", err)
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

// handleTree는 folder 모드에서 요청한 디렉토리(?path=, 비우면 루트)의 1레벨 목록을 반환한다.
// 사이드바가 폴더를 펼칠 때마다 호출하므로 전체 트리를 재귀 스캔하지 않는다.
// 오류 응답은 서버 경로가 노출되지 않도록 일반 메시지로 답한다.
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

// handleWebOpen은 URL을 전용 Chrome 창에서 연다. Chrome 미설치·잘못된 URL은 400, 기동 실패는 500.
func (s *Server) handleWebOpen(w http.ResponseWriter, r *http.Request) {
	if !s.acceptWebStateChange(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWebJSONBodyBytes)
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	// 검증·정규화를 먼저 한다. 이후 로그·기록에는 원문 대신 정규화된 URL만 남긴다.
	target, err := ValidateWebURL(body.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	launched, err := s.webOpener.Open(target)
	switch {
	case errors.Is(err, ErrInvalidWebURL), errors.Is(err, ErrChromeUnavailable):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		logging.Error("[Web] 열기 실패 (%s): %v", target, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	logging.Info("[Web] 열기: %s (launched=%v)", target, launched)
	logging.LogProcessEvent("execution", "step_ok", "웹 열기: "+target, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "launched": launched, "url": target})
}

// CloseWeb은 전용 Chrome을 종료한다 (mdview 종료 경로).
func (s *Server) CloseWeb() {
	s.webOpener.Close()
}

// handleWebStart는 녹음 시작 시각(epoch ms)을 받아 웹 이벤트 수집을 시작한다. 기존 버퍼는 폐기된다.
func (s *Server) handleWebStart(w http.ResponseWriter, r *http.Request) {
	if !s.acceptWebStateChange(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWebJSONBodyBytes)
	var body struct {
		StartEpochMs int64 `json:"startEpochMs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	now := time.Now().UnixMilli()
	if body.StartEpochMs <= 0 {
		body.StartEpochMs = now
	} else if skew := time.Duration(absInt64(body.StartEpochMs-now)) * time.Millisecond; skew > maxStartEpochSkew {
		logging.Warn("[Web] startEpochMs가 서버 시각과 %s 차이 — 서버 시각으로 대체 (받은 값 %d)", skew, body.StartEpochMs)
		body.StartEpochMs = now
	}
	s.web.Start(body.StartEpochMs)
	logging.Info("[Web] 수집 시작 (startEpochMs=%d)", body.StartEpochMs)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleWebStop은 정지 시점에 수집을 멈추고 t를 녹화 길이 안으로 클램프한다.
func (s *Server) handleWebStop(w http.ResponseWriter, r *http.Request) {
	if !s.acceptWebStateChange(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWebJSONBodyBytes)
	var body struct {
		DurationMs int64 `json:"durationMs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	count := s.web.Freeze(body.DurationMs)
	logging.Info("[Web] 수집 정지 (durationMs=%d, 이벤트 %d개)", body.DurationMs, count)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "events": count})
}

// handleWebCancel은 녹음 중이 아닐 때 남은 버퍼만 비운다.
// 웹 버퍼는 프로세스 전역이고 서버당 녹음은 하나뿐이므로, 다른 탭의 이탈이 진행 중인 녹음을
// 지우지 못하도록 녹음 중 cancel은 무시한다.
func (s *Server) handleWebCancel(w http.ResponseWriter, r *http.Request) {
	if !s.acceptWebStateChange(w, r) {
		return
	}
	if recording, _ := s.web.Live(); recording {
		logging.Warn("[Web] 녹음 중 cancel 무시")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.web.Reset()
	w.WriteHeader(http.StatusNoContent)
}

// handleWebLive는 녹음 중 1초 폴링에 Chrome 상태와 수집 건수를 답한다.
func (s *Server) handleWebLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, methodNotAllowedMessage, http.StatusMethodNotAllowed)
		return
	}
	if !isSameOrigin(r) {
		http.Error(w, forbiddenOriginMessage, http.StatusForbidden)
		return
	}
	recording, events := s.web.Live()
	writeJSON(w, http.StatusOK, map[string]any{"chrome": s.webAlive(), "recording": recording, "events": events})
}

// webAlive는 Chrome 창이 떠 있는지 반환한다.
func (s *Server) webAlive() bool {
	return s.webOpener.Alive()
}

// acceptWebStateChange는 웹 세션 상태 변경 요청의 메서드와 오리진을 검사한다.
func (s *Server) acceptWebStateChange(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, methodNotAllowedMessage, http.StatusMethodNotAllowed)
		return false
	}
	if !hasLocalOrigin(r) {
		logging.Warn("[Web] 상태 변경 요청 거부: origin=%q site=%q path=%s",
			r.Header.Get(originHeader), r.Header.Get(secFetchSiteHeader), r.URL.Path)
		http.Error(w, forbiddenOriginMessage, http.StatusForbidden)
		return false
	}
	return true
}

// handleRecording은 브라우저가 녹화 종료 시 업로드하는 오디오+이벤트 메타를 저장한다.
// 저장 위치는 folder 모드면 루트, file 모드면 대상 파일의 디렉토리이다.
func (s *Server) handleRecording(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, methodNotAllowedMessage, http.StatusMethodNotAllowed)
		return
	}
	// 크로스오리진 페이지의 임의 업로드를 차단한다 (WS Origin 검증과 동일 정책).
	// multipart POST는 preflight가 없으므로 다른 localhost 포트의 페이지도 여기서 막아야 한다.
	if !isSameOrigin(r) {
		logging.Warn("[Recording] 크로스오리진 업로드 거부: %s", r.Header.Get(originHeader))
		http.Error(w, forbiddenOriginMessage, http.StatusForbidden)
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

	// 웹 이벤트의 소유자는 서버다. 스냅샷과 동시에 수집이 멈춘다 (/api/web/stop이 실패했어도).
	// 버퍼는 저장이 성공한 뒤에만 비워, 저장 실패 후 재업로드가 같은 이벤트를 다시 병합할 수 있게 한다.
	webEvents, dropped := s.web.Snapshot(metaDurationMs(meta))
	mergeWebEvents(meta, webEvents, dropped)

	dir, audioName, err := SaveRecording(s.recordingBaseDir(), audio, header.Header.Get("Content-Type"), meta, time.Now())
	eventCount, eventsSummary := summarizeRecordingEvents(meta)
	if err != nil {
		logging.Error("[Recording] 저장 실패: %v", err)
		logging.LogRecordingUpload(string(s.mode), "-", header.Filename, header.Size,
			eventCount, eventsSummary, err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.web.Clear()

	logging.Info("[Recording] 저장: %s (%s %d bytes, 이벤트 %d개)", dir, audioName, header.Size, eventCount)
	warnIfAudioUnusable(filepath.Join(dir, audioName))
	logging.LogRecordingUpload(string(s.mode), dir, audioName, header.Size, eventCount, eventsSummary, "")

	// 후처리(전사 가능 시 전사 → prompt.txt)를 백그라운드로 시작한다.
	s.startPipeline(dir)

	writeJSON(w, http.StatusOK, RecordingResult{
		Dir:          dir,
		Audio:        audioName,
		Events:       eventsFileName,
		ID:           filepath.Base(dir),
		Transcribing: true,
	})
}

// metaDurationMs는 업로드 meta의 녹화 길이(ms)를 읽는다. 없거나 숫자가 아니면 0.
func metaDurationMs(meta map[string]any) int64 {
	if v, ok := meta["durationMs"].(float64); ok {
		return int64(v)
	}
	return 0
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// startPipeline은 후처리를 백그라운드로 시작한다 (업로드 응답은 즉시 돌려준다).
func (s *Server) startPipeline(dir string) {
	id := filepath.Base(dir)
	s.setJob(&TranscribeJob{ID: id, State: TranscribeStateRunning, Dir: dir})
	logging.Info("[Pipeline] 후처리 시작: %s", id)
	s.pending.Add(1)
	go func() {
		defer s.pending.Done()
		job := s.pipeline.Run(dir)
		job.TranscriptRel = s.relToRoot(job.Transcript)
		job.PromptRel = s.relToRoot(job.Prompt)
		s.setJob(job)
		if job.State == TranscribeStateError {
			logging.Error("[Pipeline] 후처리 실패 (%s): %s", id, job.Error)
			fmt.Fprintf(os.Stderr, "후처리 실패 (%s): %s\n", id, job.Error)
			return
		}
		logging.Info("[Pipeline] 후처리 완료: %s → %s", id, job.Prompt)
		fmt.Printf("완료 — 프롬프트 저장: %s\n", job.Prompt)
	}()
}

// relToRoot는 folder 모드에서 루트 아래 경로를 슬래시 상대 경로로 바꾼다 (뷰어에서 바로 열기용). 그 외엔 빈 문자열.
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

// WaitPipelines는 진행 중인 후처리가 모두 끝날 때까지 기다린다 (종료 경로·테스트 정리용).
func (s *Server) WaitPipelines() {
	s.pending.Wait()
}

// PipelinesPending은 아직 끝나지 않은 후처리가 있는지 반환한다 (종료 안내 출력용).
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
	// PDF는 바이너리라 WS 텍스트 프레임으로 보내지 않는다 (클라이언트가 URL로 직접 읽음).
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
