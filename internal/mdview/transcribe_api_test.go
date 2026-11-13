package mdview

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mockVerboseJSON = `{
  "task": "transcribe",
  "language": "english",
  "duration": 8.5,
  "text": "Looking at this document I will explain",
  "segments": [
    {"id": 0, "start": 0.0, "end": 4.0, "text": " Looking at this document"},
    {"id": 1, "start": 4.0, "end": 8.5, "text": " I will explain"}
  ]
}`

func TestResolveOpenAIKey(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	// 1) environment variable takes precedence
	t.Setenv(openaiKeyEnv, "env-key")
	key, err := ResolveOpenAIKey()
	if err != nil || key != "env-key" {
		t.Errorf("env key = %q, err = %v; want env-key", key, err)
	}

	// 2) no environment variable + config file
	t.Setenv(openaiKeyEnv, "")
	cfgDir := filepath.Join(home, ".config", configDirName)
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, configFileName), []byte(`{"openai_api_key":"file-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err = ResolveOpenAIKey()
	if err != nil || key != "file-key" {
		t.Errorf("file key = %q, err = %v; want file-key", key, err)
	}

	// 3) neither: an error pointing to the config path
	if err := os.Remove(filepath.Join(cfgDir, configFileName)); err != nil {
		t.Fatal(err)
	}
	_, err = ResolveOpenAIKey()
	if err == nil || !strings.Contains(err.Error(), configFileName) {
		t.Errorf("missing-key error does not mention the config file: %v", err)
	}
	if !errors.Is(err, ErrNoOpenAIKey) || !strings.HasPrefix(err.Error(), ErrNoOpenAIKey.Error()+". ") {
		t.Errorf("missing-key error does not wrap ErrNoOpenAIKey or its wording changed: %v", err)
	}
}

// newMockOpenAI creates a mock server that validates the request format and returns verbose_json.
func newMockOpenAI(t *testing.T, status int, response string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" {
			t.Errorf("path = %q, want /audio/transcriptions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("multipart parse error: %v", err)
		}
		if got := r.FormValue("model"); got != "whisper-1" {
			t.Errorf("model = %q, want whisper-1", got)
		}
		if got := r.FormValue("response_format"); got != "verbose_json" {
			t.Errorf("response_format = %q, want verbose_json", got)
		}
		if got := r.FormValue("timestamp_granularities[]"); got != "segment" {
			t.Errorf("timestamp_granularities[] = %q, want segment", got)
		}
		if _, _, err := r.FormFile("file"); err != nil {
			t.Errorf("file part missing: %v", err)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
}

func withMockAPIBase(t *testing.T, url string) {
	t.Helper()
	old := openaiAPIBase
	openaiAPIBase = url
	t.Cleanup(func() { openaiAPIBase = old })
}

func TestRunWhisperAPI(t *testing.T) {
	srv := newMockOpenAI(t, http.StatusOK, mockVerboseJSON)
	defer srv.Close()
	withMockAPIBase(t, srv.URL)

	dir := t.TempDir()
	audioPath := filepath.Join(dir, "audio.webm")
	if err := os.WriteFile(audioPath, []byte("FAKEAUDIO"), 0o644); err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(dir, "audio.json")
	if _, err := runWhisperAPI(resultPath, audioPath, "whisper-1", "", "test-key"); err != nil {
		t.Fatalf("runWhisperAPI error: %v", err)
	}

	result, err := loadWhisperResult(resultPath)
	if err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if result.Language != "english" || len(result.Segments) != 2 {
		t.Errorf("result = %+v", result)
	}
	if result.Segments[1].Start != 4.0 {
		t.Errorf("second segment start = %v, want 4.0", result.Segments[1].Start)
	}
}

func TestRunWhisperAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key"}}`))
	}))
	defer srv.Close()
	withMockAPIBase(t, srv.URL)

	dir := t.TempDir()
	audioPath := filepath.Join(dir, "audio.webm")
	if err := os.WriteFile(audioPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runWhisperAPI(filepath.Join(dir, "audio.json"), audioPath, "whisper-1", "", "bad-key")
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("error does not mention 401: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "audio.json")); !os.IsNotExist(statErr) {
		t.Error("failed response was saved as a result file")
	}
}

// Full flow with the default whisper-1 model: API transcription → timestamp-merged document.
func TestTranscribeRecordingViaAPI(t *testing.T) {
	srv := newMockOpenAI(t, http.StatusOK, mockVerboseJSON)
	defer srv.Close()
	withMockAPIBase(t, srv.URL)
	t.Setenv(openaiKeyEnv, "test-key")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "audio.webm"), append([]byte{0x1A, 0x45, 0xDF, 0xA3}, "FAKEAUDIO"...), 0o644); err != nil {
		t.Fatal(err)
	}
	eventsJSON := `{"version":1,"mode":"folder","root":"/proj","durationMs":8500,"audio":"audio.webm","events":[` +
		`{"t":1000,"type":"focus","file":"doc.md","line":3,"lineEnd":3,"text":"a line of body text"},` +
		`{"t":5000,"type":"copy","file":"main.go","line":1,"lineEnd":1,"text":"package main","selection":"package main"}]}`
	if err := os.WriteFile(filepath.Join(dir, eventsFileName), []byte(eventsJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	outPath, err := TranscribeRecording(dir, DefaultTranscribeModel, "")
	if err != nil {
		t.Fatalf("TranscribeRecording error: %v", err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := parseTranscript(t, data)
	if doc.Model != "whisper-1 (OpenAI Audio API)" || doc.Language != "english" {
		t.Errorf("model/language mismatch: %s / %s\n%s", doc.Model, doc.Language, data)
	}
	// chronological merge: speech(0s) → click(1s) → speech(4s) → copy(5s)
	wantSeq := []struct {
		kind, action, text string
	}{
		{"speech", "", "Looking at this document"},
		{"action", "click", "a line of body text"},
		{"speech", "", "I will explain"},
		{"action", "copy", "package main"},
	}
	if len(doc.Timeline) != len(wantSeq) {
		t.Fatalf("timeline length = %d, want %d\n%s", len(doc.Timeline), len(wantSeq), data)
	}
	for i, w := range wantSeq {
		got := doc.Timeline[i]
		if got.Kind != w.kind || got.Action != w.action || got.Text != w.text {
			t.Errorf("timeline[%d] = %s/%s/%q, want %s/%s/%q", i, got.Kind, got.Action, got.Text, w.kind, w.action, w.text)
		}
	}
}
