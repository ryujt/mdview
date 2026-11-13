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

// Log levels
const (
	LevelDebug = 0
	LevelInfo  = 1
	LevelWarn  = 2
	LevelError = 3
)

// Environment variable settings
const (
	envDebugMode = "DEBUG_MODE"  // text logs are emitted only when true
	envLogToFile = "LOG_TO_FILE" // written to files (session/artifacts) only when true
	envLogDir    = "LOG_DIR"     // log directory (default: logs)
	envLogLevel  = "LOG_LEVEL"   // DEBUG/INFO/WARN/ERROR (default: INFO)

	defaultLogDir      = "logs"
	maxLogAgeDays      = 7
	consoleTruncateLen = 200
	sessionSeqMax      = 999

	dirTimeFormat   = "20060102-150405"
	fullTimeFormat  = "2006-01-02 15:04:05.000"
	clockTimeFormat = "15:04:05"
)

// Tier-2 category subdirectories (mdview domain)
var categorySubdirs = []string{"whisper", "recording", "state", "process"}

// Section represents one body section of an artifact document.
type Section struct {
	Title string
	Lang  string // code fence language (may be empty)
	Body  string
}

// Logger is the singleton logger. All public APIs are exposed as package-level functions.
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

// --- Configuration ---

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

// readEnvLocked re-reads settings from environment variables (call with mu held).
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

// --- Session management ---

// StartSession starts a session. When LOG_TO_FILE=true it creates the session
// directory and category subdirectories and opens the session text log file.
// Returns the session ID (empty string when file logging is disabled).
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

	// Session directory with an auto-incrementing sequence
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
		fmt.Fprintf(os.Stderr, "[logging] failed to create session directory: %s\n", logDir)
		l.toFile = false
		return l.sessionID
	}

	for _, sub := range categorySubdirs {
		_ = os.MkdirAll(filepath.Join(l.sessionDir, sub), 0o755)
	}

	l.logFilePath = filepath.Join(l.sessionDir, fmt.Sprintf("%s_%s.log", timestamp, l.sessionID))
	f, err := os.OpenFile(l.logFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[logging] failed to create log file: %v\n", err)
		l.logFile = nil
		return l.sessionID
	}
	l.logFile = f
	l.writeFileLocked(fmt.Sprintf("=== Session Started: %s ===", time.Now().Format(fullTimeFormat)))
	l.writeFileLocked("Session ID: " + l.sessionID)
	l.writeFileLocked(strings.Repeat("=", 60))
	return l.sessionID
}

// EndSession ends the session. If process events were collected, it saves
// a Mermaid diagram, then closes the log file. Repeated calls are harmless.
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

// LogFilePath returns the path of the current session text log file.
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

// --- Automatic cleanup ---

// cleanupOldLogsLocked deletes log files older than maxAgeDays and empty directories.
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
	// Remove empty directories, deepest first
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		_ = os.Remove(d) // fails if not empty -> ignored
	}
	if removed > 0 {
		fmt.Printf("[%s] INFO  [Logger] cleaned up %[3]d logs older than %[2]d days\n",
			time.Now().Format(clockTimeFormat), maxAgeDays, removed)
	}
}

// --- Tier 1: text logs ---

// Debug writes a DEBUG-level log.
func Debug(format string, args ...any) { std.log(LevelDebug, "DEBUG", format, args...) }

// Info writes an INFO-level log.
func Info(format string, args ...any) { std.log(LevelInfo, "INFO ", format, args...) }

// Warn writes a WARN-level log.
func Warn(format string, args ...any) { std.log(LevelWarn, "WARN ", format, args...) }

// Error writes an ERROR-level log.
func Error(format string, args ...any) { std.log(LevelError, "ERROR", format, args...) }

func (l *Logger) log(level int, name, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensureConfigLocked()
	if !l.debugMode || level < l.level {
		return
	}
	msg := fmt.Sprintf(format, args...)

	// Console: truncated; file: full text
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

// --- Tier 2: artifact logs ---

// LogArtifact saves a detailed Markdown log file under a category.
// Returns the saved file path (empty string if file logging is disabled or saving fails).
// Passing this path as LogProcessEvent's ArtifactPath links it to a diagram node.
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
	statusText := "Success"
	if errMsg != "" {
		status = "err"
		statusText = "Failure"
	}
	prefix := ""
	if l.sessionID != "" {
		prefix = l.sessionID + "_"
	}
	filename := fmt.Sprintf("%s_%s%s_%03d_%s.md",
		time.Now().Format(dirTimeFormat), prefix, category, seq, status)
	path := filepath.Join(dir, filename)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s #%d\n\n## Metadata\n\n| Field | Value |\n|------|-----|\n", title, seq))
	b.WriteString(fmt.Sprintf("| Time | %s |\n", time.Now().Format(fullTimeFormat)))
	b.WriteString(fmt.Sprintf("| Session ID | %s |\n", orDash(l.sessionID)))
	for _, kv := range meta {
		b.WriteString(fmt.Sprintf("| %s | %s |\n", kv[0], kv[1]))
	}
	b.WriteString(fmt.Sprintf("| Result | %s |\n\n", statusText))

	if errMsg != "" {
		writeSection(&b, "Error", "", errMsg)
	}
	for _, s := range sections {
		writeSection(&b, s.Title, s.Lang, s.Body)
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "[logging] failed to save artifact: %v\n", err)
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

// LogWhisperCall saves transcription call (API/CLI) details under the whisper/ category.
func LogWhisperCall(backend, model, lang, audioFile string, audioBytes int64,
	request, responseSummary, responseText string, durationMs float64, errMsg string) string {
	meta := [][2]string{
		{"Backend", backend},
		{"Model", model},
		{"Language", orDash(lang)},
		{"Audio", fmt.Sprintf("%s (%d bytes)", audioFile, audioBytes)},
		{"Duration", fmt.Sprintf("%.0fms", durationMs)},
	}
	var sections []Section
	if request != "" {
		sections = append(sections, Section{Title: "Request", Body: request})
	}
	if responseSummary != "" {
		sections = append(sections, Section{Title: "Response summary", Body: responseSummary})
	}
	if responseText != "" {
		sections = append(sections, Section{Title: "Transcript text", Body: responseText})
	}
	return LogArtifact("whisper", "Whisper transcription call", meta, sections, errMsg)
}

// LogRecordingUpload saves recording upload details under the recording/ category.
func LogRecordingUpload(mode, dir, audioName string, audioBytes int64,
	eventCount int, eventsSummary string, errMsg string) string {
	meta := [][2]string{
		{"Mode", mode},
		{"Location", dir},
		{"Audio", fmt.Sprintf("%s (%d bytes)", audioName, audioBytes)},
		{"Event count", fmt.Sprintf("%d", eventCount)},
	}
	var sections []Section
	if eventsSummary != "" {
		sections = append(sections, Section{Title: "Event timeline summary", Body: eventsSummary})
	}
	return LogArtifact("recording", "Recording upload", meta, sections, errMsg)
}

// --- Tier 2: state snapshots ---

// LogStateSnapshot saves internal state as JSON under the state/ category.
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
		fmt.Fprintf(os.Stderr, "[logging] failed to save snapshot: %v\n", err)
		return ""
	}
	return path
}

// --- Process events ---

// LogProcessEvent records a process event, rendered as a Mermaid diagram at
// EndSession. Call it only from orchestrator code (workers just return results).
func LogProcessEvent(phase, eventType, nodeLabel string, opts *EventOpts) {
	std.logProcessEvent(phase, eventType, nodeLabel, opts)
}

func (l *Logger) logProcessEvent(phase, eventType, nodeLabel string, opts *EventOpts) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensureConfigLocked()
	if !l.toFile {
		return // prevents unbounded event growth in long-running servers with file logging off
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
		fmt.Fprintf(os.Stderr, "[logging] failed to save process diagram: %v\n", err)
		return
	}
	l.writeFileLocked(fmt.Sprintf("[%s] INFO  [Process] Diagram saved: %s",
		time.Now().Format(fullTimeFormat), path))
}
