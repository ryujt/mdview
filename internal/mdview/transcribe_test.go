package mdview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// transcriptForTest는 transcript.json 검증용 최소 구조이다.
type transcriptForTest struct {
	Version  int    `json:"version"`
	Mode     string `json:"mode"`
	Target   string `json:"target"`
	Model    string `json:"model"`
	Language string `json:"language"`
	Guide    struct {
		Actions map[string]string `json:"actions"`
	} `json:"guide"`
	Timeline []struct {
		Kind      string  `json:"kind"`
		Action    string  `json:"action"`
		AtMs      int64   `json:"atMs"`
		StartMs   int64   `json:"startMs"`
		EndMs     int64   `json:"endMs"`
		File      string  `json:"file"`
		Line      int     `json:"line"`
		LineEnd   int     `json:"lineEnd"`
		Approx    bool    `json:"approx"`
		Text      string  `json:"text"`
		Selection string  `json:"selection"`
		PDF       *PDFRef `json:"pdf"`
	} `json:"timeline"`
}

func parseTranscript(t *testing.T, data []byte) transcriptForTest {
	t.Helper()
	var doc transcriptForTest
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("transcript.json 파싱 실패: %v\n%s", err, data)
	}
	return doc
}

func TestFindRecordingAudio(t *testing.T) {
	dir := t.TempDir()
	// whisper 결과(audio.json)는 오디오로 취급하면 안 된다.
	if err := os.WriteFile(filepath.Join(dir, "audio.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := findRecordingAudio(dir); err == nil {
		t.Error("오디오가 없는데 에러가 발생하지 않음")
	}
	if err := os.WriteFile(filepath.Join(dir, "audio.m4a"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := findRecordingAudio(dir)
	if err != nil {
		t.Fatalf("findRecordingAudio error: %v", err)
	}
	if filepath.Base(got) != "audio.m4a" {
		t.Errorf("audio = %q, want audio.m4a", got)
	}
}

func fixtureMeta() *RecordingMeta {
	return &RecordingMeta{
		Version:    1,
		Mode:       "folder",
		Root:       "/proj",
		StartedAt:  "2026-07-18T12:00:00+09:00",
		DurationMs: 30000,
		Audio:      "audio.webm",
		Events: []RecordingEvent{
			{T: 500, Type: "open", File: "doc.md"},
			{T: 6000, Type: "focus", File: "doc.md", Line: 3, LineEnd: 3, Text: "본문 한 줄"},
			{T: 8000, Type: "word", File: "doc.md", Line: 5, LineEnd: 5, Text: "오케스트레이터"},
			{T: 12000, Type: "copy", File: "main.go", Line: 1, LineEnd: 2, Text: "package main\n\nfunc main() {}", Selection: "package main"},
			{T: 20000, Type: "focus", File: "doc.md", Line: 9, LineEnd: 12, Text: "블록 내용", Approx: true},
			{T: 26000, Type: "focus", File: "docs/sample.pdf", PDF: &PDFRef{Page: 2, X: floatPtr(0), Y: floatPtr(0.25)}},
		},
	}
}

func floatPtr(v float64) *float64 {
	return &v
}

func fixtureResult() *WhisperResult {
	return &WhisperResult{
		Language: "ko",
		Segments: []TranscriptSegment{
			{Start: 5, End: 10, Text: " 이 문서의 본문을 보면"},
			{Start: 10, End: 18, Text: " 메인 함수는 이렇게 생겼고"},
			{Start: 18, End: 25, Text: " 마지막으로 이 블록이 중요합니다"},
		},
	}
}

func TestBuildTranscriptJSON(t *testing.T) {
	data, err := buildTranscriptJSON(fixtureMeta(), fixtureResult(), localWhisperDefault)
	if err != nil {
		t.Fatalf("buildTranscriptJSON error: %v", err)
	}
	doc := parseTranscript(t, data)

	if doc.Version != 1 || doc.Mode != "folder" || doc.Target != "/proj" {
		t.Errorf("헤더 필드 불일치: %+v", doc)
	}
	if doc.Model != "openai/whisper-large-v3" || doc.Language != "ko" {
		t.Errorf("model/language 불일치: %s / %s", doc.Model, doc.Language)
	}
	for _, key := range []string{"open", "click", "select", "copy"} {
		if doc.Guide.Actions[key] == "" {
			t.Errorf("guide.actions에 %q 설명이 없음", key)
		}
	}

	// 발화 3개 + 이벤트 6개가 시간순으로 병합되고, 내부 타입이 행동 이름으로 바뀐다:
	// open(0.5s) → speech(5s) → click(6s, focus) → select(8s, word) → speech(10s)
	// → copy(12s) → speech(18s) → click(20s, focus) → click(26s, PDF focus)
	wantSeq := []struct{ kind, action string }{
		{"action", "open"}, {"speech", ""}, {"action", "click"}, {"action", "select"},
		{"speech", ""}, {"action", "copy"}, {"speech", ""}, {"action", "click"},
		{"action", "click"},
	}
	if len(doc.Timeline) != len(wantSeq) {
		t.Fatalf("timeline 길이 = %d, want %d\n%s", len(doc.Timeline), len(wantSeq), data)
	}
	for i, w := range wantSeq {
		got := doc.Timeline[i]
		if got.Kind != w.kind || got.Action != w.action {
			t.Errorf("timeline[%d] = %s/%s, want %s/%s", i, got.Kind, got.Action, w.kind, w.action)
		}
	}

	speech := doc.Timeline[1]
	if speech.StartMs != 5000 || speech.EndMs != 10000 || speech.Text != "이 문서의 본문을 보면" {
		t.Errorf("speech 항목 불일치: %+v", speech)
	}
	sel := doc.Timeline[3]
	if sel.AtMs != 8000 || sel.File != "doc.md" || sel.Line != 5 || sel.Text != "오케스트레이터" {
		t.Errorf("select 항목 불일치: %+v", sel)
	}
	cp := doc.Timeline[5]
	if cp.Selection != "package main" || cp.Line != 1 || cp.LineEnd != 2 {
		t.Errorf("copy 항목 불일치: %+v", cp)
	}
	if last := doc.Timeline[7]; !last.Approx || last.Line != 9 || last.LineEnd != 12 {
		t.Errorf("approx click 항목 불일치: %+v", last)
	}
	pdfClick := doc.Timeline[8]
	if pdfClick.File != "docs/sample.pdf" || pdfClick.PDF == nil ||
		pdfClick.PDF.Page != 2 || pdfClick.PDF.X == nil || *pdfClick.PDF.X != 0 ||
		pdfClick.PDF.Y == nil || *pdfClick.PDF.Y != 0.25 {
		t.Errorf("PDF click 항목 불일치: %+v", pdfClick)
	}
}

func TestBuildTranscriptJSONNoSegments(t *testing.T) {
	data, err := buildTranscriptJSON(fixtureMeta(), &WhisperResult{Language: "ko"}, "medium")
	if err != nil {
		t.Fatalf("buildTranscriptJSON error: %v", err)
	}
	doc := parseTranscript(t, data)
	if doc.Model != "medium" {
		t.Errorf("비기본 모델 이름이 그대로 표기되지 않음: %s", doc.Model)
	}
	if len(doc.Timeline) != 6 {
		t.Errorf("세그먼트가 없어도 이벤트 6개가 나열되어야 함 (got %d)", len(doc.Timeline))
	}
	for _, e := range doc.Timeline {
		if e.Kind != "action" {
			t.Errorf("발화 없는 녹화의 timeline에 %q가 있음", e.Kind)
		}
	}

	empty, err := buildTranscriptJSON(&RecordingMeta{}, &WhisperResult{}, localWhisperDefault)
	if err != nil {
		t.Fatalf("buildTranscriptJSON(empty) error: %v", err)
	}
	if doc := parseTranscript(t, empty); len(doc.Timeline) != 0 {
		t.Errorf("빈 녹화의 timeline은 비어야 함 (got %d)", len(doc.Timeline))
	}
	if !strings.Contains(string(empty), `"timeline": []`) {
		t.Errorf("빈 timeline은 null이 아닌 []여야 함\n%s", empty)
	}
}

// 사전 생성된 whisper JSON이 있으면 whisper 실행 없이 transcript.json까지 만들어져야 한다.
func TestTranscribeRecordingReusesExistingResult(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "audio.webm"), []byte("FAKE"), 0o644); err != nil {
		t.Fatal(err)
	}
	whisperJSON := `{"text":"안녕하세요","language":"ko","segments":[{"start":0.0,"end":2.0,"text":" 안녕하세요"}]}`
	if err := os.WriteFile(filepath.Join(dir, "audio.json"), []byte(whisperJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	eventsJSON := `{"version":1,"mode":"folder","root":"/proj","durationMs":2000,"audio":"audio.webm",` +
		`"events":[{"t":100,"type":"open","file":"doc.md"}]}`
	if err := os.WriteFile(filepath.Join(dir, eventsFileName), []byte(eventsJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	outPath, err := TranscribeRecording(dir, localWhisperDefault, "")
	if err != nil {
		t.Fatalf("TranscribeRecording error: %v", err)
	}
	if filepath.Base(outPath) != transcriptFileName {
		t.Errorf("outPath = %q, want %s", outPath, transcriptFileName)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("transcript read error: %v", err)
	}
	doc := parseTranscript(t, data)
	if doc.Model != "openai/whisper-large-v3" {
		t.Errorf("model = %q", doc.Model)
	}
	// 발화(0ms)가 open(100ms)보다 앞 — 시간순 병합
	if len(doc.Timeline) != 2 || doc.Timeline[0].Kind != "speech" || doc.Timeline[0].Text != "안녕하세요" ||
		doc.Timeline[1].Action != "open" {
		t.Errorf("timeline 불일치\n%s", data)
	}
}

// 이벤트 메타가 없어도 전사 문서는 생성되어야 한다.
func TestTranscribeRecordingWithoutEvents(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "audio.webm"), []byte("FAKE"), 0o644); err != nil {
		t.Fatal(err)
	}
	whisperJSON := `{"text":"테스트","language":"ko","segments":[{"start":0.0,"end":1.0,"text":" 테스트"}]}`
	if err := os.WriteFile(filepath.Join(dir, "audio.json"), []byte(whisperJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath, err := TranscribeRecording(dir, localWhisperDefault, "")
	if err != nil {
		t.Fatalf("TranscribeRecording error: %v", err)
	}
	data, _ := os.ReadFile(outPath)
	doc := parseTranscript(t, data)
	if len(doc.Timeline) != 1 || doc.Timeline[0].Kind != "speech" || doc.Timeline[0].Text != "테스트" {
		t.Errorf("이벤트 없는 전사 실패\n%s", data)
	}
}

// web-* 이벤트는 action 이름이 문서 이벤트와 같은 어휘(open/click/select/copy)로 바뀌고
// url·selector 등 웹 필드가 보존되어야 한다.
func TestBuildTranscriptJSONWebEvents(t *testing.T) {
	meta := &RecordingMeta{
		Version: 1, Mode: "folder", Root: "/proj", DurationMs: 30000,
		Events: []RecordingEvent{
			{T: 1000, Type: WebEventOpen, URL: "https://example.com/dash"},
			{T: 2000, Type: WebEventClick, URL: "https://example.com/dash", Title: "Dash",
				Selector: "main > span.v", Tag: "span", ID: "mau", Classes: "v hi",
				Text: "12,345", Heading: "핵심 지표", Context: "월간 활성 사용자 12,345"},
			{T: 3000, Type: WebEventDblClick, URL: "https://example.com/dash", Tag: "p", Selection: "활성"},
			{T: 4000, Type: WebEventCopy, URL: "https://example.com/dash", Tag: "p", Selection: "12,345"},
		},
	}
	data, err := buildTranscriptJSON(meta, &WhisperResult{}, DefaultTranscribeModel)
	if err != nil {
		t.Fatalf("buildTranscriptJSON error: %v", err)
	}
	var doc struct {
		Guide struct {
			Fields string `json:"fields"`
		} `json:"guide"`
		Timeline []map[string]any `json:"timeline"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("파싱 실패: %v\n%s", err, data)
	}
	wantActions := []string{"open", "click", "select", "copy"}
	if len(doc.Timeline) != len(wantActions) {
		t.Fatalf("timeline 길이 = %d, want %d\n%s", len(doc.Timeline), len(wantActions), data)
	}
	for i, want := range wantActions {
		if doc.Timeline[i]["action"] != want {
			t.Errorf("timeline[%d].action = %v, want %s", i, doc.Timeline[i]["action"], want)
		}
		if doc.Timeline[i]["url"] != "https://example.com/dash" {
			t.Errorf("timeline[%d].url = %v", i, doc.Timeline[i]["url"])
		}
		if _, has := doc.Timeline[i]["file"]; has {
			t.Errorf("timeline[%d]에 file 필드가 있음", i)
		}
	}
	click := doc.Timeline[1]
	for key, want := range map[string]string{
		"title": "Dash", "selector": "main > span.v", "tag": "span", "id": "mau",
		"classes": "v hi", "text": "12,345", "heading": "핵심 지표", "context": "월간 활성 사용자 12,345",
	} {
		if click[key] != want {
			t.Errorf("click.%s = %v, want %q", key, click[key], want)
		}
	}
	if !strings.Contains(doc.Guide.Fields, "url") {
		t.Errorf("guide.fields에 웹 이벤트 설명이 없음: %s", doc.Guide.Fields)
	}
}

func TestBuildTranscriptJSONDocEventsHaveNoWebFields(t *testing.T) {
	data, err := buildTranscriptJSON(fixtureMeta(), fixtureResult(), localWhisperDefault)
	if err != nil {
		t.Fatalf("buildTranscriptJSON error: %v", err)
	}
	var doc struct {
		Timeline []map[string]any `json:"timeline"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}
	for i, entry := range doc.Timeline {
		for _, key := range []string{"url", "title", "selector", "tag", "id", "classes", "heading", "context"} {
			if _, has := entry[key]; has {
				t.Errorf("timeline[%d]에 웹 필드 %q가 있음", i, key)
			}
		}
	}
	var top map[string]any
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}
	if _, has := top["droppedWebEvents"]; has {
		t.Error("최상위에 droppedWebEvents가 있음")
	}
}
