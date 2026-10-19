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
		t.Fatalf("%s 읽기 실패: %v", name, err)
	}
	return string(data)
}

// pipelineFixture는 오디오와 events.json이 들어 있는 녹화 디렉토리를 만든다.
func pipelineFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir, "audio.webm", "FAKEAUDIO")
	meta := RecordingMeta{
		Version: 1, Mode: "folder", Root: "/proj", StartedAt: "2026-10-18T14:03:11+09:00",
		DurationMs: 30000, Audio: "audio.webm",
		Events: []RecordingEvent{
			{T: 1000, Type: "focus", File: "doc.md", Line: 3, LineEnd: 3, Text: "본문"},
			{T: 12345, Type: WebEventClick, URL: "https://example.com", Tag: "span", Classes: "metric-value", Text: "12,345"},
		},
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, eventsFileName, string(encoded))
	return dir
}

// 키가 없어도 조작 기록만으로 prompt.txt가 만들어져야 한다.
func TestPipelineWithoutTranscription(t *testing.T) {
	t.Setenv(openaiKeyEnv, "")
	t.Setenv("HOME", t.TempDir())
	dir := pipelineFixture(t)

	job := NewPipeline().Run(dir)
	if job.State != TranscribeStateDone {
		t.Fatalf("job.State = %q (%s)", job.State, job.Error)
	}
	if job.Transcript != "" {
		t.Errorf("전사를 생략했는데 transcript 경로가 있음: %q", job.Transcript)
	}
	if !strings.Contains(job.Note, "API 키") {
		t.Errorf("job.Note에 키 없음 사유가 없음: %q", job.Note)
	}
	if _, err := os.Stat(filepath.Join(dir, transcriptFileName)); !os.IsNotExist(err) {
		t.Error("전사를 생략했는데 transcript.json이 생성됨")
	}
	if job.Prompt != filepath.Join(dir, promptFileName) {
		t.Errorf("job.Prompt = %q", job.Prompt)
	}
	text := readFixture(t, dir, promptFileName)
	if !strings.Contains(text, promptNoSpeech) || !strings.Contains(text, `[00:12] 웹 클릭: "12,345" (span.metric-value)`) {
		t.Errorf("프롬프트 내용 불일치:\n%s", text)
	}
}

// 전사 결과(audio.json)가 이미 있으면 API 없이 재사용해 transcript.json과 prompt.txt를 모두 만든다.
func TestPipelineWithExistingTranscription(t *testing.T) {
	t.Setenv(openaiKeyEnv, "sk-test")
	dir := pipelineFixture(t)
	writeFixture(t, dir, "audio.json",
		`{"text":"지표가 이상해요","language":"ko","segments":[{"start":3.0,"end":6.0,"text":" 지표가 이상해요"}]}`)

	job := NewPipeline().Run(dir)
	if job.State != TranscribeStateDone {
		t.Fatalf("job.State = %q (%s)", job.State, job.Error)
	}
	if job.Transcript != filepath.Join(dir, transcriptFileName) {
		t.Errorf("job.Transcript = %q", job.Transcript)
	}
	if job.Note != "" {
		t.Errorf("전사를 재사용했는데 job.Note = %q", job.Note)
	}
	if _, err := os.Stat(job.Transcript); err != nil {
		t.Errorf("transcript.json이 없음: %v", err)
	}
	text := readFixture(t, dir, promptFileName)
	if !strings.Contains(text, "발화 1개") || !strings.Contains(text, "[00:03] 말: 지표가 이상해요") {
		t.Errorf("발화가 반영되지 않음:\n%s", text)
	}
}

func TestPipelineFailsWithoutMeta(t *testing.T) {
	t.Setenv(openaiKeyEnv, "")
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	writeFixture(t, dir, "audio.webm", "FAKEAUDIO")
	if job := NewPipeline().Run(dir); job.State != TranscribeStateError {
		t.Errorf("%s 이 없는데 성공으로 처리됨: %q", eventsFileName, job.State)
	}
}

func TestTranscribeSkipReason(t *testing.T) {
	t.Setenv(openaiKeyEnv, "")
	t.Setenv("HOME", t.TempDir())
	dir := pipelineFixture(t)
	if reason := TranscribeSkipReason(dir, DefaultTranscribeModel); !strings.Contains(reason, "API 키") {
		t.Errorf("키 없음 사유가 아님: %q", reason)
	}
	if reason := TranscribeSkipReason(dir, localWhisperDefault); reason != "" {
		t.Errorf("로컬 모델은 키와 무관해야 함: %q", reason)
	}
	t.Setenv(openaiKeyEnv, "sk-test")
	if reason := TranscribeSkipReason(dir, DefaultTranscribeModel); reason != "" {
		t.Errorf("키가 있으면 생략하지 않아야 함: %q", reason)
	}
	if reason := TranscribeSkipReason(t.TempDir(), DefaultTranscribeModel); !strings.Contains(reason, "오디오") {
		t.Errorf("오디오 없음 사유가 아님: %q", reason)
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
		t.Error("Prompt가 비어 있음")
	}
	if job.Transcript != "" {
		t.Errorf("transcript.json이 없는데 Transcript = %q", job.Transcript)
	}
	if prompt := readFixture(t, dir, promptFileName); !strings.Contains(prompt, "메인 함수는 이렇게 생겼고") {
		t.Errorf("프롬프트에 발화가 없음:\n%s", prompt)
	}
}

func TestTranscribeSkipReasonOversizedAudio(t *testing.T) {
	t.Setenv(openaiKeyEnv, "sk-test")
	dir := pipelineFixture(t)
	if err := os.Truncate(filepath.Join(dir, "audio.webm"), maxAPIAudioBytes+1); err != nil {
		t.Fatal(err)
	}
	if reason := TranscribeSkipReason(dir, DefaultTranscribeModel); !strings.Contains(reason, "상한") {
		t.Errorf("상한 초과 사유가 아님: %q", reason)
	}
}

func TestPipelineRunDoesNotTranscribeWithoutMeta(t *testing.T) {
	t.Setenv(openaiKeyEnv, "sk-test")
	dir := t.TempDir()
	writeFixture(t, dir, "audio.webm", "FAKEAUDIO")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("전사 API가 호출됨")
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
