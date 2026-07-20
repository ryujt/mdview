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
	got := sanitizeLabel(`API "호출" (실패) {오류} | #3 <검사>` + "\n다음 줄")
	for _, banned := range []string{`"`, "(", ")", "{", "}", "|", "#", "<검사>"} {
		if strings.Contains(got, banned) {
			t.Errorf("금지 문자 %q 잔존: %q", banned, got)
		}
	}
	for _, want := range []string{"'호출'", "[실패]", "[오류]", "No.3", "&lt;검사&gt;", "<br/>"} {
		if !strings.Contains(got, want) {
			t.Errorf("치환 결과 %q 없음: %q", want, got)
		}
	}
	if sanitizeLabel("세미콜론; 구분") != "세미콜론, 구분" {
		t.Errorf("세미콜론 치환 실패: %q", sanitizeLabel("세미콜론; 구분"))
	}
}

func TestRenderSequentialFlow(t *testing.T) {
	// strategy 없는 execution들은 순차 체인으로 연결되어야 한다 (fan-out 아님).
	events := []ProcessEvent{
		{Timestamp: ts(0), Phase: "start", EventType: "start", NodeLabel: "전사 시작"},
		{Timestamp: ts(1), Phase: "execution", EventType: "step_ok", NodeLabel: "오디오 탐색"},
		{Timestamp: ts(2), Phase: "execution", EventType: "step_ok", NodeLabel: "whisper 전사"},
		{Timestamp: ts(3), Phase: "synthesis", EventType: "end", NodeLabel: "문서 생성"},
		{Timestamp: ts(4), Phase: "end", EventType: "end", NodeLabel: "완료"},
	}
	md := MermaidRenderer{}.Render(events, "abc12345", "/logs/s1")

	wants := []string{
		"# Process Flow",
		"| Session ID | `abc12345` |",
		"flowchart TD",
		`N0(["전사 시작"])`,
		`N1["오디오 탐색"]`,
		"N0 --> N1",
		"N1 --> N2", // 전략 없는 실행 단계는 순차 체인
		"N2 --> N3",
		"N3 --> N4",
		"class N1 step_ok;",
		"class N4 end_ok;",
	}
	for _, w := range wants {
		if !strings.Contains(md, w) {
			t.Errorf("렌더 결과에 %q 없음\n---\n%s", w, md)
		}
	}
	if strings.Contains(md, "N0 --> N2") {
		t.Errorf("전략 없는 실행 단계가 fan-out으로 그려짐\n%s", md)
	}
}

func TestRenderFanOutFanIn(t *testing.T) {
	events := []ProcessEvent{
		{Timestamp: ts(0), Phase: "start", EventType: "start", NodeLabel: "시작"},
		{Timestamp: ts(1), Phase: "strategy", EventType: "start", NodeLabel: "계획", Iteration: 1},
		{Timestamp: ts(2), Phase: "execution", EventType: "step_ok", NodeLabel: "작업 A", Iteration: 1},
		{Timestamp: ts(3), Phase: "execution", EventType: "step_err", NodeLabel: "작업 B", Iteration: 1},
		{Timestamp: ts(4), Phase: "synthesis", EventType: "end", NodeLabel: "종합", Iteration: 1},
		{Timestamp: ts(5), Phase: "end", EventType: "end", NodeLabel: "완료"},
	}
	md := MermaidRenderer{}.Render(events, "s", "")

	for _, w := range []string{
		"N1 --> N2", "N1 --> N3", // fan-out
		"N2 --> N4", "N3 --> N4", // fan-in
		"class N3 step_err;",
	} {
		if !strings.Contains(md, w) {
			t.Errorf("fan-out/fan-in 엣지 %q 없음\n%s", w, md)
		}
	}
}

func TestRenderDecisionBranches(t *testing.T) {
	events := []ProcessEvent{
		{Timestamp: ts(0), Phase: "start", EventType: "start", NodeLabel: "시작"},
		{Timestamp: ts(1), Phase: "decision", EventType: "branch_fail", NodeLabel: "판정 1"},
		{Timestamp: ts(2), Phase: "strategy", EventType: "start", NodeLabel: "재계획"},
		{Timestamp: ts(3), Phase: "decision", EventType: "branch_ok", NodeLabel: "판정 2"},
		{Timestamp: ts(4), Phase: "end", EventType: "end", NodeLabel: "완료"},
	}
	md := MermaidRenderer{}.Render(events, "s", "")

	if !strings.Contains(md, `N1 -->|"below threshold"| N2`) {
		t.Errorf("branch_fail → strategy 엣지 없음\n%s", md)
	}
	if !strings.Contains(md, `N3 -->|"threshold met"| N4`) {
		t.Errorf("branch_ok → end 엣지 없음\n%s", md)
	}
	if !strings.Contains(md, `N1{"판정 1"}`) {
		t.Errorf("decision 마름모 노드 없음\n%s", md)
	}
	// 무레이블 N3-->N4가 레이블 엣지로 교체되어야 한다
	if strings.Contains(md, "N3 --> N4\n") {
		t.Errorf("무레이블 엣지가 레이블 엣지로 교체되지 않음\n%s", md)
	}
}

func TestRenderArtifactLinks(t *testing.T) {
	events := []ProcessEvent{
		{Timestamp: ts(0), Phase: "start", EventType: "start", NodeLabel: "시작"},
		{Timestamp: ts(1), Phase: "execution", EventType: "step_ok", NodeLabel: "전사",
			ArtifactPath: "/logs/s1/whisper/x_ok.md"},
		{Timestamp: ts(2), Phase: "end", EventType: "end", NodeLabel: "끝"},
	}
	md := MermaidRenderer{}.Render(events, "s", "/logs/s1")

	if !strings.Contains(md, `click N1 "whisper/x_ok.md" "Open artifact"`) {
		t.Errorf("click 링크(상대 경로) 없음\n%s", md)
	}
	if !strings.Contains(md, "## Artifacts") || !strings.Contains(md, "| N1 | step_ok | `whisper/x_ok.md` |") {
		t.Errorf("Artifacts 테이블 없음\n%s", md)
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
		t.Fatalf("세션 ID 길이 = %d, want 8 (%q)", len(id), id)
	}

	// 세션 디렉토리 + 카테고리 하위 디렉토리
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("세션 디렉토리가 1개 생성되어야 함: %v", entries)
	}
	sessionDir := filepath.Join(dir, entries[0].Name())
	if !strings.HasSuffix(entries[0].Name(), "-001") {
		t.Errorf("세션 디렉토리 시퀀스 접미사 없음: %s", entries[0].Name())
	}
	for _, sub := range categorySubdirs {
		if fi, err := os.Stat(filepath.Join(sessionDir, sub)); err != nil || !fi.IsDir() {
			t.Errorf("카테고리 디렉토리 %s 없음", sub)
		}
	}

	// 1계층 텍스트 로그
	Info("테스트 메시지 %d", 42)
	logPath := LogFilePath()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("세션 로그 파일 읽기 실패: %v", err)
	}
	if !strings.Contains(string(data), "테스트 메시지 42") {
		t.Errorf("텍스트 로그에 메시지 없음:\n%s", data)
	}

	// 2계층 아티팩트: 파일명 컨벤션 + 순번 + ok/err
	p1 := LogArtifact("whisper", "Whisper 전사 호출", [][2]string{{"모델", "whisper-1"}},
		[]Section{{Title: "요청", Body: "model=whisper-1"}}, "")
	p2 := LogArtifact("whisper", "Whisper 전사 호출", nil, nil, "boom")
	if !strings.Contains(filepath.Base(p1), "_whisper_001_ok.md") {
		t.Errorf("아티팩트 파일명 컨벤션 위반: %s", p1)
	}
	if !strings.Contains(filepath.Base(p2), "_whisper_002_err.md") {
		t.Errorf("실패 아티팩트 파일명 컨벤션 위반: %s", p2)
	}
	if !strings.Contains(filepath.Base(p1), id+"_") {
		t.Errorf("파일명에 세션 ID 접두어 없음: %s", p1)
	}
	body, _ := os.ReadFile(p2)
	if !strings.Contains(string(body), "## 에러") || !strings.Contains(string(body), "boom") {
		t.Errorf("실패 아티팩트에 에러 섹션 없음:\n%s", body)
	}

	// 상태 스냅샷
	sp := LogStateSnapshot("startup", map[string]any{"mode": "folder"})
	var snap map[string]any
	rawSnap, err := os.ReadFile(sp)
	if err != nil || json.Unmarshal(rawSnap, &snap) != nil {
		t.Fatalf("스냅샷 JSON 읽기/파싱 실패: %v", err)
	}
	if snap["meta"].(map[string]any)["trigger"] != "startup" {
		t.Errorf("스냅샷 trigger 누락: %v", snap)
	}

	// 프로세스 이벤트 → EndSession 시 다이어그램 생성
	LogProcessEvent("start", "start", "세션 시작", nil)
	LogProcessEvent("execution", "step_ok", "전사", &EventOpts{ArtifactPath: p1})
	LogProcessEvent("end", "end", "세션 종료", nil)
	EndSession()

	processFiles, _ := filepath.Glob(filepath.Join(sessionDir, "process", "*_process.md"))
	if len(processFiles) != 1 {
		t.Fatalf("프로세스 다이어그램이 1개 생성되어야 함: %v", processFiles)
	}
	diagram, _ := os.ReadFile(processFiles[0])
	if !strings.Contains(string(diagram), "flowchart TD") ||
		!strings.Contains(string(diagram), "whisper/") {
		t.Errorf("다이어그램 내용 이상:\n%s", diagram)
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
		t.Error("7일 지난 로그 파일이 정리되지 않음")
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Error("빈 로그 디렉토리가 정리되지 않음")
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
		t.Errorf("파일 기록 꺼짐 상태에서 세션 ID = %q, want 빈 문자열", id)
	}
	if p := LogArtifact("whisper", "t", nil, nil, ""); p != "" {
		t.Errorf("꺼짐 상태에서 아티팩트가 저장됨: %s", p)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("꺼짐 상태에서 디렉토리 생성됨: %v", entries)
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
	Debug("디버그는 걸러짐")
	Info("인포도 걸러짐")
	Warn("워닝은 남음")
	Error("에러도 남음")

	data, err := os.ReadFile(LogFilePath())
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "걸러짐") {
		t.Errorf("레벨 필터가 동작하지 않음:\n%s", s)
	}
	if !strings.Contains(s, "워닝은 남음") || !strings.Contains(s, "에러도 남음") {
		t.Errorf("WARN 이상이 기록되지 않음:\n%s", s)
	}
}
