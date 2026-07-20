package logging

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 로그 레벨
const (
	LevelDebug = 0
	LevelInfo  = 1
	LevelWarn  = 2
	LevelError = 3
)

// 환경변수 설정
const (
	envDebugMode = "DEBUG_MODE"   // true여야 텍스트 로그가 출력됨
	envLogToFile = "LOG_TO_FILE"  // true여야 파일(세션/아티팩트)에 기록됨
	envLogDir    = "LOG_DIR"      // 로그 디렉토리 (기본 logs)
	envLogLevel  = "LOG_LEVEL"    // DEBUG/INFO/WARN/ERROR (기본 INFO)

	defaultLogDir      = "logs"
	maxLogAgeDays      = 7
	consoleTruncateLen = 200
	sessionSeqMax      = 999

	dirTimeFormat   = "20060102-150405"
	fullTimeFormat  = "2006-01-02 15:04:05.000"
	clockTimeFormat = "15:04:05"
)

// 2계층 카테고리 하위 디렉토리 (mdview 도메인)
var categorySubdirs = []string{"whisper", "recording", "state", "process"}

// Section은 아티팩트 문서의 본문 섹션 하나를 표현한다.
type Section struct {
	Title string
	Lang  string // 코드 펜스 언어 (빈 값 허용)
	Body  string
}

// Logger는 싱글톤 로거 본체이다. 모든 공개 API는 패키지 레벨 함수로 노출된다.
type Logger struct {
	mu         sync.Mutex
	configured bool

	debugMode  bool
	toFile     bool
	level      int
	logDirPath string

	sessionID    string
	sessionDir   string
	sessionStart time.Time
	logFile      *os.File
	logFilePath  string

	events   []ProcessEvent
	counters map[string]int
	renderer MermaidRenderer
}

var std = &Logger{counters: map[string]int{}}

// --- 설정 ---

func parseLevel(s string) int {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return LevelDebug
	case "INFO", "":
		return LevelInfo
	case "WARN", "WARNING":
		return LevelWarn
	case "ERROR":
		return LevelError
	}
	return LevelInfo
}

func envBool(key string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(key)), "true")
}

// readEnvLocked는 환경변수에서 설정을 다시 읽는다 (mu 보유 상태에서 호출).
func (l *Logger) readEnvLocked() {
	l.debugMode = envBool(envDebugMode)
	l.toFile = envBool(envLogToFile)
	l.level = parseLevel(os.Getenv(envLogLevel))
	l.logDirPath = os.Getenv(envLogDir)
	if l.logDirPath == "" {
		l.logDirPath = defaultLogDir
	}
	l.configured = true
}

func (l *Logger) ensureConfigLocked() {
	if !l.configured {
		l.readEnvLocked()
	}
}

func (l *Logger) logDirLocked() string {
	dir := l.logDirPath
	if !filepath.IsAbs(dir) {
		if cwd, err := os.Getwd(); err == nil {
			dir = filepath.Join(cwd, dir)
		}
	}
	return dir
}

// --- 세션 관리 ---

// StartSession은 세션을 시작한다. LOG_TO_FILE=true면 세션 디렉토리와
// 카테고리 하위 디렉토리를 생성하고 세션 텍스트 로그 파일을 연다.
// 반환값: 세션 ID (파일 기록이 꺼져 있으면 빈 문자열).
func StartSession() string { return std.startSession() }

func (l *Logger) startSession() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.readEnvLocked()

	l.events = nil
	l.counters = map[string]int{}
	l.sessionStart = time.Now()

	if !l.toFile {
		l.sessionID = ""
		l.sessionDir = ""
		return ""
	}

	l.closeFileLocked()
	l.cleanupOldLogsLocked(maxLogAgeDays)

	l.sessionID = newSessionID()
	logDir := l.logDirLocked()
	timestamp := time.Now().Format(dirTimeFormat)

	// 자동 증분 시퀀스 세션 디렉토리
	for seq := 1; seq <= sessionSeqMax; seq++ {
		candidate := filepath.Join(logDir, fmt.Sprintf("%s-%03d", timestamp, seq))
		if err := os.MkdirAll(filepath.Dir(candidate), 0o755); err != nil {
			break
		}
		if err := os.Mkdir(candidate, 0o755); err == nil {
			l.sessionDir = candidate
			break
		} else if !os.IsExist(err) {
			break
		}
	}
	if l.sessionDir == "" {
		fmt.Fprintf(os.Stderr, "[logging] 세션 디렉토리 생성 실패: %s\n", logDir)
		l.toFile = false
		return l.sessionID
	}

	for _, sub := range categorySubdirs {
		_ = os.MkdirAll(filepath.Join(l.sessionDir, sub), 0o755)
	}

	l.logFilePath = filepath.Join(l.sessionDir, fmt.Sprintf("%s_%s.log", timestamp, l.sessionID))
	f, err := os.OpenFile(l.logFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[logging] 로그 파일 생성 실패: %v\n", err)
		l.logFile = nil
		return l.sessionID
	}
	l.logFile = f
	l.writeFileLocked(fmt.Sprintf("=== Session Started: %s ===", time.Now().Format(fullTimeFormat)))
	l.writeFileLocked("Session ID: " + l.sessionID)
	l.writeFileLocked(strings.Repeat("=", 60))
	return l.sessionID
}

// EndSession은 세션을 종료한다. 수집된 프로세스 이벤트가 있으면
// Mermaid 다이어그램을 저장하고 로그 파일을 닫는다. 중복 호출은 무해하다.
func EndSession() { std.endSession() }

func (l *Logger) endSession() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.logFile != nil {
		l.writeFileLocked(strings.Repeat("=", 60))
		l.writeFileLocked(fmt.Sprintf("=== Session Ended: %s ===", time.Now().Format(fullTimeFormat)))
		l.saveProcessDiagramLocked()
		l.closeFileLocked()
	}
	l.sessionID = ""
	l.sessionDir = ""
	l.events = nil
}

// LogFilePath는 현재 세션 텍스트 로그 파일 경로를 반환한다.
func LogFilePath() string {
	std.mu.Lock()
	defer std.mu.Unlock()
	return std.logFilePath
}

func newSessionID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return time.Now().Format("150405") + "00"
	}
	return hex.EncodeToString(b)
}

// --- 자동 정리 ---

// cleanupOldLogsLocked는 maxAgeDays 이상 된 로그 파일과 빈 디렉토리를 삭제한다.
func (l *Logger) cleanupOldLogsLocked(maxAgeDays int) {
	logDir := l.logDirLocked()
	info, err := os.Stat(logDir)
	if err != nil || !info.IsDir() {
		return
	}
	cutoff := time.Now().Add(-time.Duration(maxAgeDays) * 24 * time.Hour)
	removed := 0
	var dirs []string
	_ = filepath.WalkDir(logDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != logDir {
				dirs = append(dirs, path)
			}
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().Before(cutoff) {
			if os.Remove(path) == nil {
				removed++
			}
		}
		return nil
	})
	// 깊은 디렉토리부터 빈 디렉토리 제거
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		_ = os.Remove(d) // 비어 있지 않으면 실패 → 무시
	}
	if removed > 0 {
		fmt.Printf("[%s] INFO  [Logger] %d일 지난 로그 %d개 정리\n",
			time.Now().Format(clockTimeFormat), maxAgeDays, removed)
	}
}

// --- 1계층: 텍스트 로그 ---

// Debug는 DEBUG 레벨 로그를 남긴다.
func Debug(format string, args ...any) { std.log(LevelDebug, "DEBUG", format, args...) }

// Info는 INFO 레벨 로그를 남긴다.
func Info(format string, args ...any) { std.log(LevelInfo, "INFO ", format, args...) }

// Warn은 WARN 레벨 로그를 남긴다.
func Warn(format string, args ...any) { std.log(LevelWarn, "WARN ", format, args...) }

// Error는 ERROR 레벨 로그를 남긴다.
func Error(format string, args ...any) { std.log(LevelError, "ERROR", format, args...) }

func (l *Logger) log(level int, name, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensureConfigLocked()
	if !l.debugMode || level < l.level {
		return
	}
	msg := fmt.Sprintf(format, args...)

	// 콘솔: 짧게 잘라서, 파일: 전문
	display := msg
	if runes := []rune(display); len(runes) > consoleTruncateLen {
		display = string(runes[:consoleTruncateLen]) + "..."
	}
	fmt.Printf("[%s] %s %s\n", time.Now().Format(clockTimeFormat), name, display)

	if l.toFile && l.logFile != nil {
		l.writeFileLocked(fmt.Sprintf("[%s] %s %s", time.Now().Format(fullTimeFormat), name, msg))
	}
}

func (l *Logger) writeFileLocked(line string) {
	if l.logFile == nil {
		return
	}
	if _, err := l.logFile.WriteString(line + "\n"); err == nil {
		_ = l.logFile.Sync()
	}
}

func (l *Logger) closeFileLocked() {
	if l.logFile != nil {
		_ = l.logFile.Close()
		l.logFile = nil
	}
}

// --- 2계층: 아티팩트 로그 ---

// LogArtifact는 카테고리별 상세 로그 마크다운 파일을 저장한다.
// 반환값: 저장된 파일 경로 (파일 기록이 꺼져 있거나 실패 시 빈 문자열).
// 이 경로를 LogProcessEvent의 ArtifactPath로 전달하면 다이어그램 노드와 연결된다.
func LogArtifact(category, title string, meta [][2]string, sections []Section, errMsg string) string {
	return std.logArtifact(category, title, meta, sections, errMsg)
}

func (l *Logger) logArtifact(category, title string, meta [][2]string, sections []Section, errMsg string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensureConfigLocked()
	if !l.toFile {
		return ""
	}

	dir := filepath.Join(l.logDirLocked(), category)
	if l.sessionDir != "" {
		dir = filepath.Join(l.sessionDir, category)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}

	l.counters[category]++
	seq := l.counters[category]
	status := "ok"
	statusText := "성공"
	if errMsg != "" {
		status = "err"
		statusText = "실패"
	}
	prefix := ""
	if l.sessionID != "" {
		prefix = l.sessionID + "_"
	}
	filename := fmt.Sprintf("%s_%s%s_%03d_%s.md",
		time.Now().Format(dirTimeFormat), prefix, category, seq, status)
	path := filepath.Join(dir, filename)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s #%d\n\n## 메타 정보\n\n| 항목 | 값 |\n|------|-----|\n", title, seq))
	b.WriteString(fmt.Sprintf("| 시간 | %s |\n", time.Now().Format(fullTimeFormat)))
	b.WriteString(fmt.Sprintf("| 세션 ID | %s |\n", orDash(l.sessionID)))
	for _, kv := range meta {
		b.WriteString(fmt.Sprintf("| %s | %s |\n", kv[0], kv[1]))
	}
	b.WriteString(fmt.Sprintf("| 결과 | %s |\n\n", statusText))

	if errMsg != "" {
		writeSection(&b, "에러", "", errMsg)
	}
	for _, s := range sections {
		writeSection(&b, s.Title, s.Lang, s.Body)
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "[logging] 아티팩트 저장 실패: %v\n", err)
		return ""
	}
	return path
}

func writeSection(b *strings.Builder, title, lang, body string) {
	fence := "```"
	for strings.Contains(body, fence) {
		fence += "`"
	}
	b.WriteString(fmt.Sprintf("## %s\n\n%s%s\n%s\n%s\n\n", title, fence, lang, body, fence))
}

// LogWhisperCall은 전사 호출(API/CLI) 상세를 whisper/ 카테고리에 저장한다.
func LogWhisperCall(backend, model, lang, audioFile string, audioBytes int64,
	request, responseSummary, responseText string, durationMs float64, errMsg string) string {
	meta := [][2]string{
		{"Backend", backend},
		{"모델", model},
		{"언어", orDash(lang)},
		{"오디오", fmt.Sprintf("%s (%d bytes)", audioFile, audioBytes)},
		{"소요 시간", fmt.Sprintf("%.0fms", durationMs)},
	}
	var sections []Section
	if request != "" {
		sections = append(sections, Section{Title: "요청", Body: request})
	}
	if responseSummary != "" {
		sections = append(sections, Section{Title: "응답 요약", Body: responseSummary})
	}
	if responseText != "" {
		sections = append(sections, Section{Title: "전사 텍스트", Body: responseText})
	}
	return LogArtifact("whisper", "Whisper 전사 호출", meta, sections, errMsg)
}

// LogRecordingUpload는 녹화 업로드 저장 상세를 recording/ 카테고리에 저장한다.
func LogRecordingUpload(mode, dir, audioName string, audioBytes int64,
	eventCount int, eventsSummary string, errMsg string) string {
	meta := [][2]string{
		{"모드", mode},
		{"저장 위치", dir},
		{"오디오", fmt.Sprintf("%s (%d bytes)", audioName, audioBytes)},
		{"이벤트 수", fmt.Sprintf("%d", eventCount)},
	}
	var sections []Section
	if eventsSummary != "" {
		sections = append(sections, Section{Title: "이벤트 타임라인 요약", Body: eventsSummary})
	}
	return LogArtifact("recording", "녹화 업로드", meta, sections, errMsg)
}

// --- 2계층: 상태 스냅샷 ---

// LogStateSnapshot은 내부 상태를 state/ 카테고리에 JSON으로 저장한다.
func LogStateSnapshot(trigger string, data any) string { return std.logStateSnapshot(trigger, data) }

func (l *Logger) logStateSnapshot(trigger string, data any) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensureConfigLocked()
	if !l.toFile {
		return ""
	}
	dir := filepath.Join(l.logDirLocked(), "state")
	if l.sessionDir != "" {
		dir = filepath.Join(l.sessionDir, "state")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	prefix := ""
	if l.sessionID != "" {
		prefix = l.sessionID + "_"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s_%sstate_%s.json",
		time.Now().Format(dirTimeFormat), prefix, trigger))

	snapshot := map[string]any{
		"meta": map[string]any{
			"timestamp":  time.Now().Format(fullTimeFormat),
			"session_id": l.sessionID,
			"trigger":    trigger,
		},
		"data": data,
	}
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return ""
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "[logging] 스냅샷 저장 실패: %v\n", err)
		return ""
	}
	return path
}

// --- 프로세스 이벤트 ---

// LogProcessEvent는 프로세스 이벤트를 기록한다. EndSession 시 Mermaid 다이어그램으로
// 렌더링된다. 오케스트레이터의 코드에서만 호출한다 (워커는 결과만 반환한다).
func LogProcessEvent(phase, eventType, nodeLabel string, opts *EventOpts) {
	std.logProcessEvent(phase, eventType, nodeLabel, opts)
}

func (l *Logger) logProcessEvent(phase, eventType, nodeLabel string, opts *EventOpts) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensureConfigLocked()
	if !l.toFile {
		return // 파일 기록이 꺼진 장기 실행 서버에서 이벤트가 무한히 쌓이는 것을 방지
	}
	ev := ProcessEvent{
		Timestamp: time.Now(),
		Phase:     phase,
		EventType: eventType,
		NodeLabel: nodeLabel,
	}
	if opts != nil {
		ev.ArtifactPath = opts.ArtifactPath
		ev.Iteration = opts.Iteration
		ev.DurationMs = opts.DurationMs
	}
	l.events = append(l.events, ev)
}

func (l *Logger) saveProcessDiagramLocked() {
	if !l.toFile || len(l.events) == 0 || l.sessionDir == "" {
		return
	}
	dir := filepath.Join(l.sessionDir, "process")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	prefix := ""
	if l.sessionID != "" {
		prefix = l.sessionID + "_"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s_%sprocess.md",
		time.Now().Format(dirTimeFormat), prefix))
	doc := l.renderer.Render(l.events, l.sessionID, l.sessionDir)
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "[logging] 프로세스 다이어그램 저장 실패: %v\n", err)
		return
	}
	l.writeFileLocked(fmt.Sprintf("[%s] INFO  [Process] Diagram saved: %s",
		time.Now().Format(fullTimeFormat), path))
}
