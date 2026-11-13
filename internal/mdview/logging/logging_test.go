package logging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ts(sec int) time.Time {
	return time.Date(2026, 7, 19, 1, 0, sec, 0, time.Local)
}

func TestSanitizeLabel(t *testing.T) {
	got := sanitizeLabel(`API "call" (failed) {error} | #3 <check>` + "\nnext line")
	for _, banned := range []string{`"`, "(", ")", "{", "}", "|", "#", "<check>"} {
		if strings.Contains(got, banned) {
			t.Errorf("banned character %q remains: %q", banned, got)
		}
	}
	for _, want := range []string{"'call'", "[failed]", "[error]", "No.3", "&lt;check&gt;", "<br/>"} {
		if !strings.Contains(got, want) {
			t.Errorf("replacement %q missing: %q", want, got)
		}
	}
	if sanitizeLabel("semicolon; separated") != "semicolon, separated" {
		t.Errorf("semicolon replacement failed: %q", sanitizeLabel("semicolon; separated"))
	}
}

func TestRenderSequentialFlow(t *testing.T) {
	// Executions without a strategy must be chained sequentially (not fan-out).
	events := []ProcessEvent{
		{Timestamp: ts(0), Phase: "start", EventType: "start", NodeLabel: "Start transcription"},
		{Timestamp: ts(1), Phase: "execution", EventType: "step_ok", NodeLabel: "Find audio"},
		{Timestamp: ts(2), Phase: "execution", EventType: "step_ok", NodeLabel: "whisper transcription"},
		{Timestamp: ts(3), Phase: "synthesis", EventType: "end", NodeLabel: "Generate document"},
		{Timestamp: ts(4), Phase: "end", EventType: "end", NodeLabel: "Done"},
	}
	md := MermaidRenderer{}.Render(events, "abc12345", "/logs/s1")

	wants := []string{
		"# Process Flow",
		"| Session ID | `abc12345` |",
		"flowchart TD",
		`N0(["Start transcription"])`,
		`N1["Find audio"]`,
		"N0 --> N1",
		"N1 --> N2", // execution steps without a strategy form a sequential chain
		"N2 --> N3",
		"N3 --> N4",
		"class N1 step_ok;",
		"class N4 end_ok;",
	}
	for _, w := range wants {
		if !strings.Contains(md, w) {
			t.Errorf("render output missing %q\n---\n%s", w, md)
		}
	}
	if strings.Contains(md, "N0 --> N2") {
		t.Errorf("execution steps without a strategy were drawn as fan-out\n%s", md)
	}
}

func TestRenderFanOutFanIn(t *testing.T) {
	events := []ProcessEvent{
		{Timestamp: ts(0), Phase: "start", EventType: "start", NodeLabel: "Start"},
		{Timestamp: ts(1), Phase: "strategy", EventType: "start", NodeLabel: "Plan", Iteration: 1},
		{Timestamp: ts(2), Phase: "execution", EventType: "step_ok", NodeLabel: "Task A", Iteration: 1},
		{Timestamp: ts(3), Phase: "execution", EventType: "step_err", NodeLabel: "Task B", Iteration: 1},
		{Timestamp: ts(4), Phase: "synthesis", EventType: "end", NodeLabel: "Synthesize", Iteration: 1},
		{Timestamp: ts(5), Phase: "end", EventType: "end", NodeLabel: "Done"},
	}
	md := MermaidRenderer{}.Render(events, "s", "")

	for _, w := range []string{
		"N1 --> N2", "N1 --> N3", // fan-out
		"N2 --> N4", "N3 --> N4", // fan-in
		"class N3 step_err;",
	} {
		if !strings.Contains(md, w) {
			t.Errorf("fan-out/fan-in edge %q missing\n%s", w, md)
		}
	}
}

func TestRenderDecisionBranches(t *testing.T) {
	events := []ProcessEvent{
		{Timestamp: ts(0), Phase: "start", EventType: "start", NodeLabel: "Start"},
		{Timestamp: ts(1), Phase: "decision", EventType: "branch_fail", NodeLabel: "Check 1"},
		{Timestamp: ts(2), Phase: "strategy", EventType: "start", NodeLabel: "Replan"},
		{Timestamp: ts(3), Phase: "decision", EventType: "branch_ok", NodeLabel: "Check 2"},
		{Timestamp: ts(4), Phase: "end", EventType: "end", NodeLabel: "Done"},
	}
	md := MermaidRenderer{}.Render(events, "s", "")

	if !strings.Contains(md, `N1 -->|"below threshold"| N2`) {
		t.Errorf("branch_fail -> strategy edge missing\n%s", md)
	}
	if !strings.Contains(md, `N3 -->|"threshold met"| N4`) {
		t.Errorf("branch_ok -> end edge missing\n%s", md)
	}
	if !strings.Contains(md, `N1{"Check 1"}`) {
		t.Errorf("decision diamond node missing\n%s", md)
	}
	// The unlabeled N3-->N4 must be replaced by the labeled edge
	if strings.Contains(md, "N3 --> N4\n") {
		t.Errorf("unlabeled edge was not replaced by the labeled edge\n%s", md)
	}
}

func TestRenderArtifactLinks(t *testing.T) {
	events := []ProcessEvent{
		{Timestamp: ts(0), Phase: "start", EventType: "start", NodeLabel: "Start"},
		{Timestamp: ts(1), Phase: "execution", EventType: "step_ok", NodeLabel: "Transcribe",
			ArtifactPath: "/logs/s1/whisper/x_ok.md"},
		{Timestamp: ts(2), Phase: "end", EventType: "end", NodeLabel: "End"},
	}
	md := MermaidRenderer{}.Render(events, "s", "/logs/s1")

	if !strings.Contains(md, `click N1 "whisper/x_ok.md" "Open artifact"`) {
		t.Errorf("click link (relative path) missing\n%s", md)
	}
	if !strings.Contains(md, "## Artifacts") || !strings.Contains(md, "| N1 | step_ok | `whisper/x_ok.md` |") {
		t.Errorf("Artifacts table missing\n%s", md)
	}
}

func setupEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(envDebugMode, "true")
	t.Setenv(envLogToFile, "true")
	t.Setenv(envLogLevel, "DEBUG")
	t.Setenv(envLogDir, dir)
	return dir
}

func TestSessionLifecycle(t *testing.T) {
	dir := setupEnv(t)

	id := StartSession()
	defer EndSession()
	if len(id) != 8 {
		t.Fatalf("session ID length = %d, want 8 (%q)", len(id), id)
	}

	// Session directory + category subdirectories
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("expected exactly one session directory: %v", entries)
	}
	sessionDir := filepath.Join(dir, entries[0].Name())
	if !strings.HasSuffix(entries[0].Name(), "-001") {
		t.Errorf("session directory missing sequence suffix: %s", entries[0].Name())
	}
	for _, sub := range categorySubdirs {
		if fi, err := os.Stat(filepath.Join(sessionDir, sub)); err != nil || !fi.IsDir() {
			t.Errorf("category directory %s missing", sub)
		}
	}

	// Tier-1 text log
	Info("test message %d", 42)
	logPath := LogFilePath()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read session log file: %v", err)
	}
	if !strings.Contains(string(data), "test message 42") {
		t.Errorf("message missing from text log:\n%s", data)
	}

	// Tier-2 artifacts: filename convention + sequence + ok/err
	p1 := LogArtifact("whisper", "Whisper transcription call", [][2]string{{"Model", "whisper-1"}},
		[]Section{{Title: "Request", Body: "model=whisper-1"}}, "")
	p2 := LogArtifact("whisper", "Whisper transcription call", nil, nil, "boom")
	if !strings.Contains(filepath.Base(p1), "_whisper_001_ok.md") {
		t.Errorf("artifact filename convention violated: %s", p1)
	}
	if !strings.Contains(filepath.Base(p2), "_whisper_002_err.md") {
		t.Errorf("failed artifact filename convention violated: %s", p2)
	}
	if !strings.Contains(filepath.Base(p1), id+"_") {
		t.Errorf("filename missing session ID prefix: %s", p1)
	}
	body, _ := os.ReadFile(p2)
	if !strings.Contains(string(body), "## Error") || !strings.Contains(string(body), "boom") {
		t.Errorf("failed artifact missing Error section:\n%s", body)
	}

	// State snapshot
	sp := LogStateSnapshot("startup", map[string]any{"mode": "folder"})
	var snap map[string]any
	rawSnap, err := os.ReadFile(sp)
	if err != nil || json.Unmarshal(rawSnap, &snap) != nil {
		t.Fatalf("failed to read/parse snapshot JSON: %v", err)
	}
	if snap["meta"].(map[string]any)["trigger"] != "startup" {
		t.Errorf("snapshot missing trigger: %v", snap)
	}

	// Process events -> diagram generated at EndSession
	LogProcessEvent("start", "start", "Session start", nil)
	LogProcessEvent("execution", "step_ok", "Transcribe", &EventOpts{ArtifactPath: p1})
	LogProcessEvent("end", "end", "Session end", nil)
	EndSession()

	processFiles, _ := filepath.Glob(filepath.Join(sessionDir, "process", "*_process.md"))
	if len(processFiles) != 1 {
		t.Fatalf("expected exactly one process diagram: %v", processFiles)
	}
	diagram, _ := os.ReadFile(processFiles[0])
	if !strings.Contains(string(diagram), "flowchart TD") ||
		!strings.Contains(string(diagram), "whisper/") {
		t.Errorf("unexpected diagram content:\n%s", diagram)
	}
}

func TestCleanupOldLogs(t *testing.T) {
	dir := setupEnv(t)

	oldDir := filepath.Join(dir, "20260101-000000-001")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(oldDir, "old.log")
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(oldFile, past, past); err != nil {
		t.Fatal(err)
	}

	StartSession()
	EndSession()

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("log file older than 7 days was not cleaned up")
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Error("empty log directory was not cleaned up")
	}
}

func TestDisabledWithoutEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDebugMode, "false")
	t.Setenv(envLogToFile, "false")
	t.Setenv(envLogDir, dir)

	id := StartSession()
	defer EndSession()
	if id != "" {
		t.Errorf("session ID with file logging off = %q, want empty string", id)
	}
	if p := LogArtifact("whisper", "t", nil, nil, ""); p != "" {
		t.Errorf("artifact saved while file logging off: %s", p)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("directory created while file logging off: %v", entries)
	}
}

func TestLogLevelFiltering(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envDebugMode, "true")
	t.Setenv(envLogToFile, "true")
	t.Setenv(envLogLevel, "WARN")
	t.Setenv(envLogDir, dir)

	StartSession()
	defer EndSession()
	Debug("debug is filtered out")
	Info("info is filtered out too")
	Warn("warning is kept")
	Error("error is kept too")

	data, err := os.ReadFile(LogFilePath())
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "filtered out") {
		t.Errorf("level filter did not work:\n%s", s)
	}
	if !strings.Contains(s, "warning is kept") || !strings.Contains(s, "error is kept too") {
		t.Errorf("WARN and above were not logged:\n%s", s)
	}
}
