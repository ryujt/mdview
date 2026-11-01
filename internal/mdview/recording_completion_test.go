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
	t.Setenv("HOME", t.TempDir())
	dir := pipelineFixture(t)
	writeFixture(t, dir, "audio.json", `{"text":"한글과 이모지 🎙️","language":"ko","segments":[{"start":0,"end":1,"text":"한글과 이모지 🎙️"}]}`)
	s := newServer(ModeFolder, NewHub())
	var copied []byte
	var notifications []string
	s.completion = &recordingCompletion{
		copyText: func(data []byte) error { copied = bytes.Clone(data); return nil },
		notify: func(title, message string) error {
			if len(copied) == 0 {
				return errors.New("알림 전에 복사가 완료되지 않음")
			}
			notifications = append(notifications, title+": "+message)
			return nil
		},
	}
	s.startPipeline(dir)
	s.WaitPipelines()
	job := s.transcribeJobs[filepath.Base(dir)]
	if job.State != TranscribeStateDone || !job.ClipboardCopied || job.ClipboardError != "" || job.NotificationError != "" {
		t.Fatalf("완료 상태 불일치: %+v", job)
	}
	want := readFixture(t, dir, transcriptFileName)
	if string(copied) != want {
		t.Fatalf("transcript.json 전체 내용과 클립보드가 다름: %s", copied)
	}
	if len(notifications) != 1 || !strings.Contains(notifications[0], "클립보드에 복사") {
		t.Fatalf("알림 불일치: %v", notifications)
	}
	// 프로젝트 밖 결과도 기존 파일 API의 접근 범위를 넓히지 않고 열 수 있다.
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
			t.Fatalf("저장 결과 열기 실패: %s status=%d err=%v", name, res.StatusCode, err)
		}
	}
	for _, query := range []string{"?id=unknown&name=transcript.json", "?id=" + url.QueryEscape(job.ID) + "&name=events.json", "?id=" + url.QueryEscape(job.ID) + "&name=../audio.json"} {
		res, err := http.Get(ts.URL + apiRecordingFilePath + query)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("허용하지 않은 파일에 접근함: %s (%d)", query, res.StatusCode)
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
		t.Errorf("다른 origin에서 결과 접근: %d", res.StatusCode)
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
				if job.ClipboardCopied || job.ClipboardError == "" || strings.Contains(message, "복사했습니다") {
					t.Fatalf("복사 실패를 성공으로 표시함: %+v, %s", job, message)
				}
			}
			if mode == "no-transcript" && (copied || job.ClipboardCopied || job.ClipboardError != "") {
				t.Fatalf("전사 없이 클립보드를 변경함: %+v", job)
			}
			if mode == "notification" && (!job.ClipboardCopied || job.NotificationError == "") {
				t.Fatalf("알림 실패 상태 불일치: %+v", job)
			}
			if mode == "prompt-error" {
				if !job.ClipboardCopied || job.State != TranscribeStateError {
					t.Fatalf("프롬프트 저장 실패로 전사 복사를 생략함: %+v", job)
				}
			} else if job.State != TranscribeStateDone {
				t.Fatal("클립보드/알림 오류가 저장 성공 상태를 변경함")
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
		t.Fatalf("클립보드 바이트 불일치: %q (%v)", data, err)
	}
	message := "한글 알림 \"quoted\"\n두 번째 줄"
	if err := notifyDesktop("mdview", message); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(notificationFile)
	if err != nil || !strings.HasSuffix(string(data), "mdview\n"+message+"\n") {
		t.Fatalf("알림 argv 불일치: %q (%v)", data, err)
	}
}
