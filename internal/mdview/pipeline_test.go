package mdview

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), recordingFilePerm); err != nil {
		t.Fatalf("fixture write error (%s): %v", name, err)
	}
}

func readFixture(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("failed to read %s: %v", name, err)
	}
	return string(data)
}

// pipelineFixture creates a recording directory containing audio and events.json.
func pipelineFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir, "audio.webm", "FAKEAUDIO")
	meta := RecordingMeta{
		Version: 1, Mode: "folder", Root: "/proj", StartedAt: "2026-10-18T14:03:11+09:00",
		DurationMs: 30000, Audio: "audio.webm",
		Events: []RecordingEvent{
			{T: 1000, Type: "focus", File: "doc.md", Line: 3, LineEnd: 3, Text: "body"},
			{T: 12345, Type: "copy", File: "doc.md", Line: 5, LineEnd: 5, Text: "12,345", Selection: "12,345"},
		},
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, eventsFileName, string(encoded))
	return dir
}

// Without a key, prompt.txt should still be built from the action log alone.
func TestPipelineWithoutTranscription(t *testing.T) {
	t.Setenv(openaiKeyEnv, "")
	setTestHome(t, t.TempDir())
	dir := pipelineFixture(t)

	job := NewPipeline().Run(dir)
	if job.State != TranscribeStateDone {
		t.Fatalf("job.State = %q (%s)", job.State, job.Error)
	}
	if job.Transcript != "" {
		t.Errorf("transcription was skipped but a transcript path is set: %q", job.Transcript)
	}
	if !strings.Contains(job.Note, ErrNoOpenAIKey.Error()) {
		t.Errorf("job.Note lacks the missing-key reason: %q", job.Note)
	}
	if _, err := os.Stat(filepath.Join(dir, transcriptFileName)); !os.IsNotExist(err) {
		t.Error("transcription was skipped but transcript.json was created")
	}
	if job.Prompt != filepath.Join(dir, promptFileName) {
		t.Errorf("job.Prompt = %q", job.Prompt)
	}
	text := readFixture(t, dir, promptFileName)
	if !strings.Contains(text, promptNoSpeech) || !strings.Contains(text, `[00:12] Copy: "12,345" (doc.md:5)`) {
		t.Errorf("prompt content mismatch:\n%s", text)
	}
}

// If a transcription result (audio.json) exists, reuse it without the API and build both transcript.json and prompt.txt.
func TestPipelineWithExistingTranscription(t *testing.T) {
	t.Setenv(openaiKeyEnv, "sk-test")
	dir := pipelineFixture(t)
	writeFixture(t, dir, "audio.json",
		`{"text":"The metrics look wrong","language":"en","segments":[{"start":3.0,"end":6.0,"text":" The metrics look wrong"}]}`)

	job := NewPipeline().Run(dir)
	if job.State != TranscribeStateDone {
		t.Fatalf("job.State = %q (%s)", job.State, job.Error)
	}
	if job.Transcript != filepath.Join(dir, transcriptFileName) {
		t.Errorf("job.Transcript = %q", job.Transcript)
	}
	if job.Note != "" {
		t.Errorf("transcription was reused but job.Note = %q", job.Note)
	}
	if _, err := os.Stat(job.Transcript); err != nil {
		t.Errorf("transcript.json missing: %v", err)
	}
	text := readFixture(t, dir, promptFileName)
	if !strings.Contains(text, "Speech segments: 1") || !strings.Contains(text, "[00:03] Speech: The metrics look wrong") {
		t.Errorf("speech not reflected:\n%s", text)
	}
}

func TestPipelineFailsWithoutMeta(t *testing.T) {
	t.Setenv(openaiKeyEnv, "")
	setTestHome(t, t.TempDir())
	dir := t.TempDir()
	writeFixture(t, dir, "audio.webm", "FAKEAUDIO")
	if job := NewPipeline().Run(dir); job.State != TranscribeStateError {
		t.Errorf("%s is missing but treated as success: %q", eventsFileName, job.State)
	}
}

func TestTranscribeSkipReason(t *testing.T) {
	t.Setenv(openaiKeyEnv, "")
	setTestHome(t, t.TempDir())
	dir := pipelineFixture(t)
	if reason := TranscribeSkipReason(dir, DefaultTranscribeModel); !strings.Contains(reason, ErrNoOpenAIKey.Error()) {
		t.Errorf("not a missing-key reason: %q", reason)
	}
	if reason := TranscribeSkipReason(dir, localWhisperDefault); reason != "" {
		t.Errorf("local models should not depend on the key: %q", reason)
	}
	t.Setenv(openaiKeyEnv, "sk-test")
	if reason := TranscribeSkipReason(dir, DefaultTranscribeModel); reason != "" {
		t.Errorf("should not skip when a key is present: %q", reason)
	}
	if reason := TranscribeSkipReason(t.TempDir(), DefaultTranscribeModel); !strings.Contains(reason, "audio") {
		t.Errorf("not a missing-audio reason: %q", reason)
	}
}

func TestRunAfterTranscribeWithoutTranscriptFile(t *testing.T) {
	dir := pipelineFixture(t)
	result, err := json.Marshal(fixtureResult())
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "audio.json", string(result))

	job := NewPipeline().RunAfterTranscribe(dir)
	if job.State != TranscribeStateDone {
		t.Fatalf("State = %v, error=%q", job.State, job.Error)
	}
	if job.Prompt == "" {
		t.Error("Prompt is empty")
	}
	if job.Transcript != "" {
		t.Errorf("transcript.json is absent but Transcript = %q", job.Transcript)
	}
	if prompt := readFixture(t, dir, promptFileName); !strings.Contains(prompt, "the main function looks like this") {
		t.Errorf("prompt has no speech:\n%s", prompt)
	}
}

func TestTranscribeSkipReasonOversizedAudio(t *testing.T) {
	t.Setenv(openaiKeyEnv, "sk-test")
	dir := pipelineFixture(t)
	if err := os.Truncate(filepath.Join(dir, "audio.webm"), maxAPIAudioBytes+1); err != nil {
		t.Fatal(err)
	}
	if reason := TranscribeSkipReason(dir, DefaultTranscribeModel); !strings.Contains(reason, "limit") {
		t.Errorf("not an over-limit reason: %q", reason)
	}
}

func TestPipelineRunDoesNotTranscribeWithoutMeta(t *testing.T) {
	t.Setenv(openaiKeyEnv, "sk-test")
	dir := t.TempDir()
	writeFixture(t, dir, "audio.webm", "FAKEAUDIO")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("transcription API was called")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	old := openaiAPIBase
	openaiAPIBase = srv.URL
	t.Cleanup(func() { openaiAPIBase = old })

	if job := NewPipeline().Run(dir); job.State != TranscribeStateError {
		t.Errorf("State = %v, want error", job.State)
	}
}
