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

// newTestServer는 folder 모드 서버를 띄운다. 전사가 실제 API를 부르지 않도록 키와 HOME을 비운다.
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
		t.Fatalf("응답 디코드 실패: %v", err)
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
	t.Fatalf("후처리가 시간 안에 끝나지 않음: %s", id)
	return nil
}

// 업로드한 문서 이벤트가 events.json에 그대로 저장되고, 후처리가 prompt.txt를 만든다.
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
		t.Error("후처리가 시작되지 않음")
	}

	data, err := os.ReadFile(filepath.Join(result.Dir, eventsFileName))
	if err != nil {
		t.Fatalf("events.json 읽기 실패: %v", err)
	}
	var saved RecordingMeta
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("events.json 파싱 실패: %v", err)
	}
	wantTypes := []string{"open", "focus", "word"}
	if len(saved.Events) != len(wantTypes) {
		t.Fatalf("events = %d개, want %d개: %+v", len(saved.Events), len(wantTypes), saved.Events)
	}
	for i, want := range wantTypes {
		if saved.Events[i].Type != want {
			t.Errorf("events[%d].type = %s, want %s", i, saved.Events[i].Type, want)
		}
	}
	if saved.Events[1].File != "a.md" || saved.Events[1].T != 1500 || saved.Events[1].Text != "x" {
		t.Errorf("문서 이벤트 내용 불일치: %+v", saved.Events[1])
	}

	job := ts.waitForJob(t, result.ID)
	if job.State != TranscribeStateDone {
		t.Fatalf("job.State = %q (%s)", job.State, job.Error)
	}
	if job.Prompt != filepath.Join(result.Dir, promptFileName) {
		t.Errorf("job.Prompt = %q", job.Prompt)
	}
	if job.PromptRel != "" {
		t.Errorf("프로젝트 밖의 녹화에 상대 경로가 있음: %q", job.PromptRel)
	}
	if job.Transcript != "" || job.TranscriptRel != "" {
		t.Errorf("전사 없이 transcript 경로가 채워짐: %+v", job)
	}
	text, err := os.ReadFile(job.Prompt)
	if err != nil || !strings.Contains(string(text), `[00:01] 클릭: "x" (a.md:2)`) {
		t.Errorf("prompt.txt 내용 불일치 (%v):\n%s", err, text)
	}
}

// DNS 리바인딩: evil.com이 127.0.0.1을 가리키게 된 페이지는 Origin과 Host를 모두 evil.com:<port>로 맞출 수 있다.
// 호스트:포트 일치만으로는 통과하므로 로컬 호스트 이름 조건과 전역 Host 가드가 함께 막아야 한다.
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
		t.Errorf("리바인딩 settings status = %d, want 403", res.StatusCode)
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
		t.Errorf("리바인딩 업로드 status = %d, want 403", res.StatusCode)
	}

	// Origin 없는 기존 GET 엔드포인트도 전역 Host 가드가 막는다.
	req, _ = http.NewRequest(http.MethodGet, ts.URL+apiModePath, nil)
	req.Host = "evil.com:1234"
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("mode error: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("로컬이 아닌 Host의 /api/mode status = %d, want 403", res.StatusCode)
	}

	// 정상 요청(127.0.0.1)과 localhost Host는 통과한다.
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

// isSameOrigin 단위 검증 (전역 가드 없이): 로컬 호스트 이름과 호스트:포트 일치를 모두 요구한다.
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
			t.Fatalf("서버가 응답하지 않음: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	server.StopHTTP(time.Second)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StopHTTP 후 Start가 반환하지 않음")
	}
}
