package mdview

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAudioExtFromMime(t *testing.T) {
	cases := []struct {
		contentType string
		want        string
	}{
		{"audio/webm;codecs=opus", ".webm"},
		{"audio/webm", ".webm"},
		{"video/webm;codecs=opus", ".webm"},
		{"audio/mp4", ".m4a"},
		{"audio/ogg;codecs=opus", ".ogg"},
		{"audio/mpeg", ".mp3"},
		{"", ".webm"},
		{"application/octet-stream", ".webm"},
		{"invalid;;", ".webm"},
	}
	for _, c := range cases {
		if got := AudioExtFromMime(c.contentType); got != c.want {
			t.Errorf("AudioExtFromMime(%q) = %q, want %q", c.contentType, got, c.want)
		}
	}
}

func TestSaveRecording(t *testing.T) {
	base := t.TempDir()
	now := time.Date(2026, 7, 18, 12, 34, 56, 0, time.Local)
	meta := map[string]any{"version": float64(1), "mode": "folder", "events": []any{}}

	dir, audioName, err := SaveRecording(base, strings.NewReader("FAKEAUDIO"), "audio/webm;codecs=opus", meta, now)
	if err != nil {
		t.Fatalf("SaveRecording error: %v", err)
	}

	wantDir := filepath.Join(base, recordingsDirName, "rec-20260718-123456")
	if dir != wantDir {
		t.Errorf("dir = %q, want %q", dir, wantDir)
	}
	if audioName != "audio.webm" {
		t.Errorf("audioName = %q, want audio.webm", audioName)
	}

	audioData, err := os.ReadFile(filepath.Join(dir, audioName))
	if err != nil {
		t.Fatalf("audio read error: %v", err)
	}
	if string(audioData) != "FAKEAUDIO" {
		t.Errorf("audio content = %q", audioData)
	}

	eventsData, err := os.ReadFile(filepath.Join(dir, eventsFileName))
	if err != nil {
		t.Fatalf("events read error: %v", err)
	}
	var saved map[string]any
	if err := json.Unmarshal(eventsData, &saved); err != nil {
		t.Fatalf("events unmarshal error: %v", err)
	}
	if saved["audio"] != "audio.webm" {
		t.Errorf("meta audio = %v, want audio.webm", saved["audio"])
	}
	if saved["savedAt"] == nil {
		t.Error("meta savedAt not injected")
	}
	if saved["mode"] != "folder" {
		t.Errorf("meta mode = %v, want folder (원본 필드 보존)", saved["mode"])
	}
}

func TestSaveRecordingUniqueDir(t *testing.T) {
	base := t.TempDir()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.Local)

	dir1, _, err := SaveRecording(base, strings.NewReader("a"), "audio/webm", map[string]any{}, now)
	if err != nil {
		t.Fatalf("first SaveRecording error: %v", err)
	}
	dir2, _, err := SaveRecording(base, strings.NewReader("b"), "audio/webm", map[string]any{}, now)
	if err != nil {
		t.Fatalf("second SaveRecording error: %v", err)
	}
	if dir1 == dir2 {
		t.Errorf("동일 타임스탬프 저장이 같은 디렉토리를 반환: %q", dir1)
	}
	if filepath.Base(dir2) != filepath.Base(dir1)+"-2" {
		t.Errorf("dir2 = %q, want %q suffix -2", dir2, dir1)
	}
}

// buildRecordingForm은 audio 파트(Content-Type 지정)와 meta 필드를 가진 multipart 본문을 만든다.
func buildRecordingForm(t *testing.T, audioContent, audioMime, meta string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	head := make(textproto.MIMEHeader)
	head.Set("Content-Disposition", `form-data; name="audio"; filename="audio"`)
	head.Set("Content-Type", audioMime)
	part, err := mw.CreatePart(head)
	if err != nil {
		t.Fatalf("create part error: %v", err)
	}
	if _, err := part.Write([]byte(audioContent)); err != nil {
		t.Fatalf("part write error: %v", err)
	}
	if err := mw.WriteField(recordingMetaField, meta); err != nil {
		t.Fatalf("write field error: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart close error: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

func TestHandleRecordingFolderMode(t *testing.T) {
	t.Setenv(openaiKeyEnv, "")
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	server, err := NewFolderServer(NewHub(), root)
	if err != nil {
		t.Fatalf("NewFolderServer error: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	defer func() {
		server.WaitPipelines()
		ts.Close()
	}()

	meta := `{"version":1,"mode":"folder","durationMs":1234,"events":[{"t":0,"type":"open","file":"README.md"}]}`
	body, contentType := buildRecordingForm(t, "AUDIOBYTES", "audio/webm;codecs=opus", meta)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+apiRecordingPath, body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", ts.URL)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}

	var result RecordingResult
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		t.Fatalf("response decode error: %v", err)
	}
	if !strings.HasPrefix(result.Dir, filepath.Join(root, recordingsDirName)) {
		t.Errorf("dir = %q, want under %s", result.Dir, filepath.Join(root, recordingsDirName))
	}

	audioData, err := os.ReadFile(filepath.Join(result.Dir, result.Audio))
	if err != nil {
		t.Fatalf("saved audio read error: %v", err)
	}
	if string(audioData) != "AUDIOBYTES" {
		t.Errorf("saved audio = %q", audioData)
	}

	eventsData, err := os.ReadFile(filepath.Join(result.Dir, result.Events))
	if err != nil {
		t.Fatalf("saved events read error: %v", err)
	}
	var saved map[string]any
	if err := json.Unmarshal(eventsData, &saved); err != nil {
		t.Fatalf("saved events unmarshal error: %v", err)
	}
	if saved["audio"] != result.Audio {
		t.Errorf("saved meta audio = %v, want %v", saved["audio"], result.Audio)
	}
	events, ok := saved["events"].([]any)
	if !ok || len(events) != 1 {
		t.Errorf("saved events = %v, want 1개", saved["events"])
	}
}

func TestHandleRecordingFileMode(t *testing.T) {
	t.Setenv(openaiKeyEnv, "")
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	workDir := t.TempDir()
	target := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(target, []byte("# doc"), 0o644); err != nil {
		t.Fatalf("fixture write error: %v", err)
	}
	server, err := NewFileServer(NewHub(), target, workDir)
	if err != nil {
		t.Fatalf("NewFileServer error: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	defer func() {
		server.WaitPipelines()
		ts.Close()
	}()

	body, contentType := buildRecordingForm(t, "X", "audio/mp4", `{"mode":"file"}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+apiRecordingPath, body)
	req.Header.Set("Content-Type", contentType)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var result RecordingResult
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		t.Fatalf("response decode error: %v", err)
	}
	// file 모드는 mdview를 실행한 터미널의 작업 디렉토리 아래 저장된다.
	if !strings.HasPrefix(result.Dir, filepath.Join(workDir, recordingsDirName)) {
		t.Errorf("dir = %q, want under %s", result.Dir, filepath.Join(workDir, recordingsDirName))
	}
	if result.Audio != "audio.m4a" {
		t.Errorf("audio = %q, want audio.m4a", result.Audio)
	}
}

func TestRecordingBaseDirFileModeFallback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(target, []byte("# doc"), 0o644); err != nil {
		t.Fatalf("fixture write error: %v", err)
	}
	// workDir가 비어 있으면 대상 파일의 디렉토리로 폴백한다.
	server, err := NewFileServer(NewHub(), target, "")
	if err != nil {
		t.Fatalf("NewFileServer error: %v", err)
	}
	if got := server.recordingBaseDir(); got != dir {
		t.Errorf("recordingBaseDir() = %q, want %q", got, dir)
	}
}

func TestHandleRecordingRejections(t *testing.T) {
	t.Setenv(openaiKeyEnv, "")
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	server, err := NewFolderServer(NewHub(), root)
	if err != nil {
		t.Fatalf("NewFolderServer error: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	defer func() {
		server.WaitPipelines()
		ts.Close()
	}()

	// GET 요청 거부
	res, err := http.Get(ts.URL + apiRecordingPath)
	if err != nil {
		t.Fatalf("GET error: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", res.StatusCode)
	}

	// 크로스오리진 업로드 거부
	body, contentType := buildRecordingForm(t, "X", "audio/webm", `{}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+apiRecordingPath, body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", "https://evil.example.com")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cross-origin request error: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin status = %d, want 403", res.StatusCode)
	}

	// 같은 호스트의 다른 포트(전용 Chrome의 개발 서버 등)에서 온 업로드 거부
	body, contentType = buildRecordingForm(t, "X", "audio/webm", `{}`)
	req, _ = http.NewRequest(http.MethodPost, ts.URL+apiRecordingPath, body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", "http://localhost:3000")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("other-port request error: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("other-port status = %d, want 403", res.StatusCode)
	}

	// meta가 JSON 객체가 아니면 거부 (배열, null 모두)
	for _, badMeta := range []string{`[1,2,3]`, `null`, `"str"`, ``} {
		body, contentType = buildRecordingForm(t, "X", "audio/webm", badMeta)
		req, _ = http.NewRequest(http.MethodPost, ts.URL+apiRecordingPath, body)
		req.Header.Set("Content-Type", contentType)
		res, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("bad meta request error: %v", err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("meta=%q status = %d, want 400", badMeta, res.StatusCode)
		}
	}

	// 거부 케이스에서 디렉토리가 생성되지 않아야 한다
	if _, err := os.Stat(filepath.Join(root, recordingsDirName)); !os.IsNotExist(err) {
		t.Errorf("거부된 요청이 %s 디렉토리를 생성함", recordingsDirName)
	}
}
