package mdview

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type testServer struct {
	*httptest.Server
	server *Server
	root   string
}

// newTestServer starts a folder-mode server. It clears the key and HOME so transcription never calls the real API.
func newTestServer(t *testing.T) *testServer {
	t.Helper()
	t.Setenv(openaiKeyEnv, "")
	setTestHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	server, err := NewFolderServer(NewHub(), root)
	if err != nil {
		t.Fatalf("NewFolderServer error: %v", err)
	}
	server.completion = testRecordingCompletion()
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(func() {
		server.WaitPipelines()
		ts.Close()
	})
	return &testServer{Server: ts, server: server, root: root}
}

func (ts *testServer) post(t *testing.T, path, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if headers == nil {
		headers = map[string]string{originHeader: ts.URL}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s error: %v", path, err)
	}
	return res
}

func (ts *testServer) get(t *testing.T, path string) *http.Response {
	t.Helper()
	res, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s error: %v", path, err)
	}
	return res
}

func decodeJSON[T any](t *testing.T, res *http.Response) T {
	t.Helper()
	defer res.Body.Close()
	var v T
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return v
}

func (ts *testServer) waitForJob(t *testing.T, id string) *TranscribeJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res := ts.get(t, apiRecordingStatusPath+"?id="+url.QueryEscape(id))
		if res.StatusCode == http.StatusOK {
			job := decodeJSON[TranscribeJob](t, res)
			if job.State != TranscribeStateRunning {
				return &job
			}
		} else {
			res.Body.Close()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("post-processing did not finish in time: %s", id)
	return nil
}

// Uploaded document events are saved as-is to events.json, and post-processing writes prompt.txt.
func TestHandleRecordingSavesEventsAndRunsPipeline(t *testing.T) {
	ts := newTestServer(t)

	meta := `{"version":1,"mode":"folder","root":"/proj","startedAt":"2026-10-18T14:03:11+09:00","durationMs":5000,` +
		`"events":[{"t":1000,"type":"open","file":"a.md"},{"t":1500,"type":"focus","file":"a.md","line":2,"lineEnd":2,"text":"x"},{"t":3000,"type":"word","file":"a.md","line":4,"lineEnd":4,"text":"y"}]}`
	body, contentType := buildRecordingForm(t, "AUDIOBYTES", "audio/webm;codecs=opus", meta)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+apiRecordingPath, body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set(originHeader, ts.URL)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload error: %v", err)
	}
	result := decodeJSON[RecordingResult](t, res)
	if !result.Transcribing {
		t.Error("post-processing did not start")
	}

	data, err := os.ReadFile(filepath.Join(result.Dir, eventsFileName))
	if err != nil {
		t.Fatalf("failed to read events.json: %v", err)
	}
	var saved RecordingMeta
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("failed to parse events.json: %v", err)
	}
	wantTypes := []string{"open", "focus", "word"}
	if len(saved.Events) != len(wantTypes) {
		t.Fatalf("events = %d, want %d: %+v", len(saved.Events), len(wantTypes), saved.Events)
	}
	for i, want := range wantTypes {
		if saved.Events[i].Type != want {
			t.Errorf("events[%d].type = %s, want %s", i, saved.Events[i].Type, want)
		}
	}
	if saved.Events[1].File != "a.md" || saved.Events[1].T != 1500 || saved.Events[1].Text != "x" {
		t.Errorf("document event mismatch: %+v", saved.Events[1])
	}

	job := ts.waitForJob(t, result.ID)
	if job.State != TranscribeStateDone {
		t.Fatalf("job.State = %q (%s)", job.State, job.Error)
	}
	if job.Prompt != filepath.Join(result.Dir, promptFileName) {
		t.Errorf("job.Prompt = %q", job.Prompt)
	}
	if job.PromptRel != "" {
		t.Errorf("recording outside the project has a relative path: %q", job.PromptRel)
	}
	if job.Transcript != "" || job.TranscriptRel != "" {
		t.Errorf("transcript path set without transcription: %+v", job)
	}
	text, err := os.ReadFile(job.Prompt)
	if err != nil || !strings.Contains(string(text), `[00:01] Click: "x" (a.md:2)`) {
		t.Errorf("prompt.txt content mismatch (%v):\n%s", err, text)
	}
}

// DNS rebinding: a page whose evil.com resolves to 127.0.0.1 can set both Origin and Host to evil.com:<port>.
// A host:port match alone would pass, so the local host name rule and the global Host guard must block it together.
func TestRejectsDNSRebindingHost(t *testing.T) {
	ts := newTestServer(t)
	port := ts.Listener.Addr().(*net.TCPAddr).Port
	evilHost := "evil.com:" + strconv.Itoa(port)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+apiSettingsPath, strings.NewReader(`{"transcribe_model":"whisper-1"}`))
	req.Host = evilHost
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(originHeader, "http://"+evilHost)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("settings error: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("rebinding settings status = %d, want 403", res.StatusCode)
	}

	body, contentType := buildRecordingForm(t, "X", "audio/webm", `{}`)
	req, _ = http.NewRequest(http.MethodPost, ts.URL+apiRecordingPath, body)
	req.Host = evilHost
	req.Header.Set("Content-Type", contentType)
	req.Header.Set(originHeader, "http://"+evilHost)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload error: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("rebinding upload status = %d, want 403", res.StatusCode)
	}

	// The global Host guard also blocks existing GET endpoints without Origin.
	req, _ = http.NewRequest(http.MethodGet, ts.URL+apiModePath, nil)
	req.Host = "evil.com:1234"
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("mode error: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("/api/mode with non-local Host status = %d, want 403", res.StatusCode)
	}

	// Normal requests (127.0.0.1) and the localhost Host pass.
	res = ts.get(t, apiModePath)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("127.0.0.1 /api/mode status = %d, want 200", res.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodGet, ts.URL+apiModePath, nil)
	req.Host = "localhost:" + strconv.Itoa(port)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("localhost mode error: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("localhost Host /api/mode status = %d, want 200", res.StatusCode)
	}
}

// isSameOrigin unit check (without the global guard): requires both a local host name and a matching host:port.
func TestIsSameOriginRequiresLocalHostname(t *testing.T) {
	cases := []struct {
		host, origin string
		want         bool
	}{
		{"127.0.0.1:8642", "", true},
		{"localhost:8642", "http://localhost:8642", true},
		{"LOCALHOST:8642", "http://localhost:8642", true},
		{"[::1]:8642", "http://[::1]:8642", true},
		{"evil.com:8642", "http://evil.com:8642", false},
		{"localhost:8642", "http://localhost:3000", false},
		{"localhost:8642", "http://evil.com:8642", false},
		{"localhost:8642", "::bad", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set(originHeader, c.origin)
		}
		if got := isSameOrigin(r); got != c.want {
			t.Errorf("isSameOrigin(host=%q, origin=%q) = %v, want %v", c.host, c.origin, got, c.want)
		}
	}
}

func TestStopHTTPBeforeStartIsNoop(t *testing.T) {
	server, err := NewFolderServer(NewHub(), t.TempDir())
	if err != nil {
		t.Fatalf("NewFolderServer error: %v", err)
	}
	server.StopHTTP(time.Second)
}

func TestStopHTTPStopsServing(t *testing.T) {
	server, err := NewFolderServer(NewHub(), t.TempDir())
	if err != nil {
		t.Fatalf("NewFolderServer error: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	done := make(chan struct{})
	go func() {
		server.Start(listener)
		close(done)
	}()
	base := "http://" + listener.Addr().String()
	deadline := time.Now().Add(2 * time.Second)
	for {
		res, err := http.Get(base + apiModePath)
		if err == nil {
			res.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not respond: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	server.StopHTTP(time.Second)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after StopHTTP")
	}
}
