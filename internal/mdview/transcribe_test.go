package mdview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// transcriptForTest is a minimal structure for verifying transcript.json.
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
		t.Fatalf("failed to parse transcript.json: %v\n%s", err, data)
	}
	return doc
}

func TestFindRecordingAudio(t *testing.T) {
	dir := t.TempDir()
	// The whisper result (audio.json) must not be treated as audio.
	if err := os.WriteFile(filepath.Join(dir, "audio.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := findRecordingAudio(dir); err == nil {
		t.Error("no error even though there is no audio")
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
			{T: 6000, Type: "focus", File: "doc.md", Line: 3, LineEnd: 3, Text: "a line of body text"},
			{T: 8000, Type: "word", File: "doc.md", Line: 5, LineEnd: 5, Text: "orchestrator"},
			{T: 12000, Type: "copy", File: "main.go", Line: 1, LineEnd: 2, Text: "package main\n\nfunc main() {}", Selection: "package main"},
			{T: 20000, Type: "focus", File: "doc.md", Line: 9, LineEnd: 12, Text: "block content", Approx: true},
			{T: 26000, Type: "focus", File: "docs/sample.pdf", PDF: &PDFRef{Page: 2, X: floatPtr(0), Y: floatPtr(0.25)}},
		},
	}
}

func floatPtr(v float64) *float64 {
	return &v
}

func fixtureResult() *WhisperResult {
	return &WhisperResult{
		Language: "en",
		Segments: []TranscriptSegment{
			{Start: 5, End: 10, Text: " Looking at the body of this document"},
			{Start: 10, End: 18, Text: " the main function looks like this"},
			{Start: 18, End: 25, Text: " and finally this block is important"},
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
		t.Errorf("header field mismatch: %+v", doc)
	}
	if doc.Model != "openai/whisper-large-v3" || doc.Language != "en" {
		t.Errorf("model/language mismatch: %s / %s", doc.Model, doc.Language)
	}
	for _, key := range []string{"open", "click", "select", "copy"} {
		if doc.Guide.Actions[key] == "" {
			t.Errorf("guide.actions has no description for %q", key)
		}
	}

	// 3 speech segments + 6 events are merged chronologically, and internal types become action names:
	// open(0.5s) → speech(5s) → click(6s, focus) → select(8s, word) → speech(10s)
	// → copy(12s) → speech(18s) → click(20s, focus) → click(26s, PDF focus)
	wantSeq := []struct{ kind, action string }{
		{"action", "open"}, {"speech", ""}, {"action", "click"}, {"action", "select"},
		{"speech", ""}, {"action", "copy"}, {"speech", ""}, {"action", "click"},
		{"action", "click"},
	}
	if len(doc.Timeline) != len(wantSeq) {
		t.Fatalf("timeline length = %d, want %d\n%s", len(doc.Timeline), len(wantSeq), data)
	}
	for i, w := range wantSeq {
		got := doc.Timeline[i]
		if got.Kind != w.kind || got.Action != w.action {
			t.Errorf("timeline[%d] = %s/%s, want %s/%s", i, got.Kind, got.Action, w.kind, w.action)
		}
	}

	speech := doc.Timeline[1]
	if speech.StartMs != 5000 || speech.EndMs != 10000 || speech.Text != "Looking at the body of this document" {
		t.Errorf("speech entry mismatch: %+v", speech)
	}
	sel := doc.Timeline[3]
	if sel.AtMs != 8000 || sel.File != "doc.md" || sel.Line != 5 || sel.Text != "orchestrator" {
		t.Errorf("select entry mismatch: %+v", sel)
	}
	cp := doc.Timeline[5]
	if cp.Selection != "package main" || cp.Line != 1 || cp.LineEnd != 2 {
		t.Errorf("copy entry mismatch: %+v", cp)
	}
	if last := doc.Timeline[7]; !last.Approx || last.Line != 9 || last.LineEnd != 12 {
		t.Errorf("approx click entry mismatch: %+v", last)
	}
	pdfClick := doc.Timeline[8]
	if pdfClick.File != "docs/sample.pdf" || pdfClick.PDF == nil ||
		pdfClick.PDF.Page != 2 || pdfClick.PDF.X == nil || *pdfClick.PDF.X != 0 ||
		pdfClick.PDF.Y == nil || *pdfClick.PDF.Y != 0.25 {
		t.Errorf("PDF click entry mismatch: %+v", pdfClick)
	}
}

func TestBuildTranscriptJSONNoSegments(t *testing.T) {
	data, err := buildTranscriptJSON(fixtureMeta(), &WhisperResult{Language: "en"}, "medium")
	if err != nil {
		t.Fatalf("buildTranscriptJSON error: %v", err)
	}
	doc := parseTranscript(t, data)
	if doc.Model != "medium" {
		t.Errorf("non-default model name not shown verbatim: %s", doc.Model)
	}
	if len(doc.Timeline) != 6 {
		t.Errorf("6 events should be listed even without segments (got %d)", len(doc.Timeline))
	}
	for _, e := range doc.Timeline {
		if e.Kind != "action" {
			t.Errorf("timeline of a recording without speech contains %q", e.Kind)
		}
	}

	empty, err := buildTranscriptJSON(&RecordingMeta{}, &WhisperResult{}, localWhisperDefault)
	if err != nil {
		t.Fatalf("buildTranscriptJSON(empty) error: %v", err)
	}
	if doc := parseTranscript(t, empty); len(doc.Timeline) != 0 {
		t.Errorf("timeline of an empty recording should be empty (got %d)", len(doc.Timeline))
	}
	if !strings.Contains(string(empty), `"timeline": []`) {
		t.Errorf("empty timeline should be [] rather than null\n%s", empty)
	}
}

// With a pre-generated whisper JSON, transcript.json should be built without running whisper.
func TestTranscribeRecordingReusesExistingResult(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "audio.webm"), []byte("FAKE"), 0o644); err != nil {
		t.Fatal(err)
	}
	whisperJSON := `{"text":"Hello","language":"en","segments":[{"start":0.0,"end":2.0,"text":" Hello"}]}`
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
	// speech (0ms) precedes open (100ms) — chronological merge
	if len(doc.Timeline) != 2 || doc.Timeline[0].Kind != "speech" || doc.Timeline[0].Text != "Hello" ||
		doc.Timeline[1].Action != "open" {
		t.Errorf("timeline mismatch\n%s", data)
	}
}

// The transcript should be generated even without event metadata.
func TestTranscribeRecordingWithoutEvents(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "audio.webm"), []byte("FAKE"), 0o644); err != nil {
		t.Fatal(err)
	}
	whisperJSON := `{"text":"test","language":"en","segments":[{"start":0.0,"end":1.0,"text":" test"}]}`
	if err := os.WriteFile(filepath.Join(dir, "audio.json"), []byte(whisperJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath, err := TranscribeRecording(dir, localWhisperDefault, "")
	if err != nil {
		t.Fatalf("TranscribeRecording error: %v", err)
	}
	data, _ := os.ReadFile(outPath)
	doc := parseTranscript(t, data)
	if len(doc.Timeline) != 1 || doc.Timeline[0].Kind != "speech" || doc.Timeline[0].Text != "test" {
		t.Errorf("transcription without events failed\n%s", data)
	}
}

// Web page interaction (web-*) events left in events.json of older recordings and unknown types
// are excluded from the timeline without error, and web-only fields do not appear in the output.
func TestBuildTranscriptJSONSkipsUnknownEvents(t *testing.T) {
	raw := `{"version":1,"mode":"folder","root":"/proj","durationMs":30000,"droppedWebEvents":2,"events":[` +
		`{"t":1000,"type":"web-open","url":"https://example.com/dash"},` +
		`{"t":2000,"type":"web-click","url":"https://example.com/dash","selector":"main > span","tag":"span","text":"12,345"},` +
		`{"t":2500,"type":"zoom","file":"a.md"},` +
		`{"t":3000,"type":"focus","file":"a.md","line":3,"lineEnd":3,"text":"body"}]}`
	var meta RecordingMeta
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatalf("failed to parse events.json: %v", err)
	}
	data, err := buildTranscriptJSON(&meta, &WhisperResult{}, DefaultTranscribeModel)
	if err != nil {
		t.Fatalf("buildTranscriptJSON error: %v", err)
	}
	var doc struct {
		Timeline []map[string]any `json:"timeline"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse failed: %v\n%s", err, data)
	}
	if len(doc.Timeline) != 1 || doc.Timeline[0]["action"] != "click" || doc.Timeline[0]["file"] != "a.md" {
		t.Fatalf("timeline = %v, want 1 document click", doc.Timeline)
	}
	for _, key := range []string{"url", "selector", "tag", "droppedWebEvents"} {
		if strings.Contains(string(data), `"`+key+`"`) {
			t.Errorf("web field %q remains in transcript.json:\n%s", key, data)
		}
	}
}
