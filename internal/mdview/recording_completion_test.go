package mdview

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testRecordingCompletion() *recordingCompletion {
	return &recordingCompletion{
		copyText: func([]byte) error { return nil },
		notify:   func(string, string) error { return nil },
	}
}

func TestRecordingPipelineCopiesTranscriptBeforeNotification(t *testing.T) {
	t.Setenv(openaiKeyEnv, "")
	setTestHome(t, t.TempDir())
	dir := pipelineFixture(t)
	writeFixture(t, dir, "audio.json", `{"text":"한글과 이모지 🎙️","language":"ko","segments":[{"start":0,"end":1,"text":"한글과 이모지 🎙️"}]}`)
	s := newServer(ModeFolder, NewHub())
	var copied []byte
	var notifications []string
	s.completion = &recordingCompletion{
		copyText: func(data []byte) error { copied = bytes.Clone(data); return nil },
		notify: func(title, message string) error {
			if len(copied) == 0 {
				return errors.New("copy did not complete before notification")
			}
			notifications = append(notifications, title+": "+message)
			return nil
		},
	}
	s.startPipeline(dir)
	s.WaitPipelines()
	job := s.transcribeJobs[filepath.Base(dir)]
	if job.State != TranscribeStateDone || !job.ClipboardCopied || job.ClipboardError != "" || job.NotificationError != "" {
		t.Fatalf("completion state mismatch: %+v", job)
	}
	want := readFixture(t, dir, transcriptFileName)
	if string(copied) != want {
		t.Fatalf("clipboard differs from full transcript.json content: %s", copied)
	}
	if len(notifications) != 1 || !strings.Contains(notifications[0], "to the clipboard") {
		t.Fatalf("notification mismatch: %v", notifications)
	}
	// Results outside the project can be opened without widening the file API's access scope.
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	for _, name := range []string{transcriptFileName, promptFileName} {
		res, err := http.Get(ts.URL + apiRecordingFilePath + "?id=" + url.QueryEscape(job.ID) + "&name=" + name)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || res.StatusCode != http.StatusOK || string(data) != readFixture(t, dir, name) {
			t.Fatalf("failed to open saved result: %s status=%d err=%v", name, res.StatusCode, err)
		}
	}
	for _, query := range []string{"?id=unknown&name=transcript.json", "?id=" + url.QueryEscape(job.ID) + "&name=events.json", "?id=" + url.QueryEscape(job.ID) + "&name=../audio.json"} {
		res, err := http.Get(ts.URL + apiRecordingFilePath + query)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("accessed a disallowed file: %s (%d)", query, res.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+apiRecordingFilePath+"?id="+url.QueryEscape(job.ID)+"&name=transcript.json", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("result accessed from another origin: %d", res.StatusCode)
	}
}

func TestRecordingCompletionFailures(t *testing.T) {
	for _, mode := range []string{"copy", "missing", "no-transcript", "notification", "prompt-error"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			transcript := filepath.Join(dir, transcriptFileName)
			writeFixture(t, dir, transcriptFileName, "{\n  \"text\": \"한글 🎙️\"\n}\n")
			job := &TranscribeJob{State: TranscribeStateDone, Transcript: transcript}
			if mode == "missing" {
				job.Transcript = filepath.Join(dir, "missing.json")
			}
			if mode == "no-transcript" {
				job.Transcript = ""
			}
			if mode == "prompt-error" {
				job.State = TranscribeStateError
				job.Error = "prompt write error"
			}
			var message string
			copied := false
			completion := &recordingCompletion{
				copyText: func([]byte) error {
					copied = true
					if mode == "copy" {
						return errors.New("clipboard unavailable")
					}
					return nil
				},
				notify: func(_, body string) error {
					message = body
					if mode == "notification" {
						return errors.New("notifications unavailable")
					}
					return nil
				},
			}
			completion.complete(job)
			if mode == "copy" || mode == "missing" {
				if job.ClipboardCopied || job.ClipboardError == "" || strings.Contains(message, "Copied") {
					t.Fatalf("copy failure reported as success: %+v, %s", job, message)
				}
			}
			if mode == "no-transcript" && (copied || job.ClipboardCopied || job.ClipboardError != "") {
				t.Fatalf("clipboard changed without a transcript: %+v", job)
			}
			if mode == "notification" && (!job.ClipboardCopied || job.NotificationError == "") {
				t.Fatalf("notification failure state mismatch: %+v", job)
			}
			if mode == "prompt-error" {
				if !job.ClipboardCopied || job.State != TranscribeStateError {
					t.Fatalf("transcript copy skipped because prompt save failed: %+v", job)
				}
			} else if job.State != TranscribeStateDone {
				t.Fatal("clipboard/notification error changed the saved-success state")
			}
		})
	}
}

func TestDesktopCommandsPreserveTranscriptBytes(t *testing.T) {
	if runtime.GOOS != osDarwin {
		t.Skip("macOS desktop gateways")
	}
	dir := t.TempDir()
	clipboardFile := filepath.Join(dir, "clipboard")
	notificationFile := filepath.Join(dir, "notification")
	t.Setenv("MDVIEW_TEST_CLIPBOARD", clipboardFile)
	t.Setenv("MDVIEW_TEST_NOTIFICATION", notificationFile)
	t.Setenv("PATH", dir)
	if err := os.WriteFile(filepath.Join(dir, "pbcopy"), []byte("#!/bin/sh\n/bin/cat > \"$MDVIEW_TEST_CLIPBOARD\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "osascript"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MDVIEW_TEST_NOTIFICATION\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	want := []byte("{\n  \"text\": \"한글 🎙️ \\\"quoted\\\"\"\n}\n")
	if err := copyDesktopText(want); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(clipboardFile)
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("clipboard bytes mismatch: %q (%v)", data, err)
	}
	message := "한글 알림 \"quoted\"\n두 번째 줄"
	if err := notifyDesktop("mdview", message); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(notificationFile)
	if err != nil || !strings.HasSuffix(string(data), "mdview\n"+message+"\n") {
		t.Fatalf("notification argv mismatch: %q (%v)", data, err)
	}
}
