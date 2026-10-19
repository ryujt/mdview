package mdview

import (
	"encoding/json"
	"errors"
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

type webTestServer struct {
	*httptest.Server
	server *Server
	root   string
}

// newWebTestServer는 folder 모드 서버를 띄운다. 전사가 실제 API를 부르지 않도록 키와 HOME을 비운다.
func newWebTestServer(t *testing.T) *webTestServer {
	t.Helper()
	t.Setenv(openaiKeyEnv, "")
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	server, err := NewFolderServer(NewHub(), root)
	if err != nil {
		t.Fatalf("NewFolderServer error: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(func() {
		server.WaitPipelines()
		ts.Close()
	})
	return &webTestServer{Server: ts, server: server, root: root}
}

// recentWebStartEpochMs는 서버의 시작 시각 보정(maxStartEpochSkew)에 걸리지 않는 최근 시각을 돌려준다.
// 2분 전으로 잡아 start+90000 같은 웹 이벤트도 현재 시각 클램프에 걸리지 않게 한다.
func recentWebStartEpochMs() int64 {
	return time.Now().Add(-2 * time.Minute).UnixMilli()
}

func (ts *webTestServer) post(t *testing.T, path, body string, headers map[string]string) *http.Response {
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

func (ts *webTestServer) get(t *testing.T, path string) *http.Response {
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

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

func (ts *webTestServer) waitForJob(t *testing.T, id string) *TranscribeJob {
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

func TestWebSessionLifecycleAPI(t *testing.T) {
	ts := newWebTestServer(t)
	start := recentWebStartEpochMs()

	res := ts.post(t, apiWebStartPath, `{"startEpochMs":`+itoa(start)+`}`, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d, want 200", res.StatusCode)
	}
	ts.server.web.Add(webClickPayload(start+1000, "div.card", "https://example.com"))
	ts.server.web.Add(webClickPayload(start+90000, "div.late", "https://example.com"))

	live := decodeJSON[map[string]any](t, ts.get(t, apiWebLivePath))
	if live["recording"] != true || live["events"] != float64(2) || live["chrome"] != false {
		t.Errorf("live = %v, want recording:true events:2 chrome:false", live)
	}

	stop := decodeJSON[map[string]any](t, ts.post(t, apiWebStopPath, `{"durationMs":60000}`, nil))
	if stop["ok"] != true || stop["events"] != float64(2) {
		t.Errorf("stop = %v", stop)
	}
	ts.server.web.Add(webClickPayload(start+61000, "div.after", "https://example.com"))
	live = decodeJSON[map[string]any](t, ts.get(t, apiWebLivePath))
	if live["recording"] != false || live["events"] != float64(2) {
		t.Errorf("정지 후 live = %v", live)
	}

	res = ts.post(t, apiWebCancelPath, "", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("cancel status = %d, want 204", res.StatusCode)
	}
	live = decodeJSON[map[string]any](t, ts.get(t, apiWebLivePath))
	if live["events"] != float64(0) {
		t.Errorf("cancel 후 live = %v", live)
	}
}

// 상태 변경 POST는 Origin 헤더가 존재하고 로컬일 것을 요구한다 (업로드보다 엄격).
func TestWebAPIRejectsUntrustedOriginAndMethods(t *testing.T) {
	ts := newWebTestServer(t)
	for _, path := range []string{apiWebStartPath, apiWebStopPath, apiWebCancelPath} {
		for _, h := range []map[string]string{
			{"Content-Type": "application/json"},
			{originHeader: "https://evil.example.com"},
			{originHeader: ts.URL, secFetchSiteHeader: "cross-site"},
		} {
			res := ts.post(t, path, "{}", h)
			res.Body.Close()
			if res.StatusCode != http.StatusForbidden {
				t.Errorf("%s (헤더 %v) status = %d, want 403", path, h, res.StatusCode)
			}
		}
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s error: %v", path, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("GET %s status = %d, want 405", path, res.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+apiWebLivePath, nil)
	req.Header.Set(originHeader, "https://evil.example.com")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("live error: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("크로스오리진 live status = %d, want 403", res.StatusCode)
	}
}

// 업로드 시 서버가 수집한 웹 이벤트가 문서 이벤트와 t순으로 병합되고, 후처리가 prompt.txt를 만든다.
func TestHandleRecordingMergesWebEventsAndRunsPipeline(t *testing.T) {
	ts := newWebTestServer(t)
	start := recentWebStartEpochMs()
	ts.post(t, apiWebStartPath, `{"startEpochMs":`+itoa(start)+`}`, nil).Body.Close()
	ts.server.web.Add(webClickPayload(start+1500, "div.card", "https://example.com"))
	ts.server.web.Add(webPayload(map[string]any{"type": "bogus"})) // dropped 1
	ts.post(t, apiWebStopPath, `{"durationMs":5000}`, nil).Body.Close()

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
	wantTypes := []string{"open", "focus", WebEventClick, "word"} // 같은 t=1500이면 문서 이벤트가 먼저
	if len(saved.Events) != len(wantTypes) {
		t.Fatalf("events = %d개, want %d개: %+v", len(saved.Events), len(wantTypes), saved.Events)
	}
	for i, want := range wantTypes {
		if saved.Events[i].Type != want {
			t.Errorf("events[%d].type = %s, want %s", i, saved.Events[i].Type, want)
		}
	}
	if saved.Events[2].Selector != "div.card" || saved.Events[2].T != 1500 {
		t.Errorf("웹 이벤트 내용 불일치: %+v", saved.Events[2])
	}
	if saved.DroppedWebEvents != 1 {
		t.Errorf("droppedWebEvents = %d, want 1", saved.DroppedWebEvents)
	}
	if _, count := ts.server.web.Live(); count != 0 {
		t.Errorf("업로드 후에도 버퍼에 %d개 남음", count)
	}

	job := ts.waitForJob(t, result.ID)
	if job.State != TranscribeStateDone {
		t.Fatalf("job.State = %q (%s)", job.State, job.Error)
	}
	if job.Prompt != filepath.Join(result.Dir, promptFileName) {
		t.Errorf("job.Prompt = %q", job.Prompt)
	}
	wantRel, _ := filepath.Rel(ts.root, job.Prompt)
	if job.PromptRel != filepath.ToSlash(wantRel) {
		t.Errorf("job.PromptRel = %q, want %q", job.PromptRel, wantRel)
	}
	if job.Transcript != "" || job.TranscriptRel != "" {
		t.Errorf("전사 없이 transcript 경로가 채워짐: %+v", job)
	}
	text, err := os.ReadFile(job.Prompt)
	if err != nil || !strings.Contains(string(text), `웹 클릭`) {
		t.Errorf("prompt.txt 내용 불일치 (%v):\n%s", err, text)
	}
}

func TestMergeWebEventsWithoutDocEvents(t *testing.T) {
	meta := map[string]any{"mode": "file"}
	mergeWebEvents(meta, []RecordingEvent{{T: 10, Type: WebEventClick, URL: "https://a"}}, 0)
	events, ok := meta["events"].([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("events = %v", meta["events"])
	}
	if events[0].(map[string]any)["url"] != "https://a" {
		t.Errorf("병합된 이벤트 = %v", events[0])
	}
	if _, has := meta[jsonKeyDroppedWebEvents]; has {
		t.Error("dropped 0인데 droppedWebEvents가 기록됨")
	}
}

func TestWebOpenAPIErrors(t *testing.T) {
	ts := newWebTestServer(t)
	ts.server.webOpener.launch = func(string) (*WebBrowser, error) { return nil, ErrChromeUnavailable }

	cases := []struct {
		body string
		want int
	}{
		{`{"url":"file:///etc/passwd"}`, http.StatusBadRequest},
		{`{"url":"https://user:pw@example.com"}`, http.StatusBadRequest},
		{`{"url":""}`, http.StatusBadRequest},
		{`not json`, http.StatusBadRequest},
		{`{"url":"https://example.com"}`, http.StatusBadRequest}, // Chrome 없음도 400 + 안내
	}
	for _, c := range cases {
		res := ts.post(t, apiWebOpenPath, c.body, nil)
		res.Body.Close()
		if res.StatusCode != c.want {
			t.Errorf("open %s status = %d, want %d", c.body, res.StatusCode, c.want)
		}
	}
	ts.server.webOpener.launch = func(string) (*WebBrowser, error) { return nil, errors.New("boom") }
	res := ts.post(t, apiWebOpenPath, `{"url":"https://example.com"}`, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Errorf("기동 실패 status = %d, want 500", res.StatusCode)
	}
	res = ts.post(t, apiWebOpenPath, `{"url":"https://example.com"}`, map[string]string{})
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("Origin 없는 open status = %d, want 403", res.StatusCode)
	}
}

// 같은 호스트의 다른 포트(전용 Chrome에서 흔히 열리는 개발 서버)는 상태 변경을 할 수 없다.
func TestWebStartRejectsOtherPortSameSite(t *testing.T) {
	ts := newWebTestServer(t)
	res := ts.post(t, apiWebStartPath, `{}`, map[string]string{
		originHeader: "http://127.0.0.1:1", secFetchSiteHeader: "same-site",
	})
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("다른 포트 start status = %d, want 403", res.StatusCode)
	}
	if recording, _ := ts.server.web.Live(); recording {
		t.Error("거부된 start가 수집을 시작함")
	}
}

func TestWebLiveAcceptsSameOrigin(t *testing.T) {
	ts := newWebTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+apiWebLivePath, nil)
	req.Header.Set(originHeader, ts.URL)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("live error: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("같은 오리진 live status = %d, want 200", res.StatusCode)
	}
}

// 녹음 중 cancel(다른 탭의 이탈 등)은 진행 중인 버퍼를 지우지 않는다. 정지 후 cancel만 비운다.
func TestWebCancelIgnoredWhileRecording(t *testing.T) {
	ts := newWebTestServer(t)
	ts.post(t, apiWebStartPath, `{"startEpochMs":`+itoa(time.Now().UnixMilli())+`}`, nil).Body.Close()
	ts.server.web.Add(webClickPayload(time.Now().UnixMilli(), "div.card", "https://example.com"))

	res := ts.post(t, apiWebCancelPath, "", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("녹음 중 cancel status = %d, want 204", res.StatusCode)
	}
	live := decodeJSON[map[string]any](t, ts.get(t, apiWebLivePath))
	if live["recording"] != true || live["events"] != float64(1) {
		t.Errorf("녹음 중 cancel 후 live = %v, want recording:true events:1", live)
	}

	ts.post(t, apiWebStopPath, `{"durationMs":60000}`, nil).Body.Close()
	ts.post(t, apiWebCancelPath, "", nil).Body.Close()
	live = decodeJSON[map[string]any](t, ts.get(t, apiWebLivePath))
	if live["events"] != float64(0) {
		t.Errorf("정지 후 cancel 후 live = %v, want events:0", live)
	}
}

// 브라우저 시계가 크게 틀린 startEpochMs는 서버 시각으로 대체된다.
func TestWebStartReplacesImplausibleEpoch(t *testing.T) {
	ts := newWebTestServer(t)
	res := ts.post(t, apiWebStartPath, `{"startEpochMs":1}`, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d, want 200", res.StatusCode)
	}
	start := ts.server.web.StartEpochMs()
	if diff := time.Now().UnixMilli() - start; diff < 0 || diff > 5000 {
		t.Errorf("startEpochMs = %d, 현재와 %dms 차이 (서버 시각으로 대체돼야 함)", start, diff)
	}
}

// 상한을 넘는 JSON 본문은 400으로 거부된다.
func TestWebJSONBodyLimit(t *testing.T) {
	ts := newWebTestServer(t)
	big := `{"durationMs":1,"pad":"` + strings.Repeat("x", maxWebJSONBodyBytes) + `"}`
	res := ts.post(t, apiWebStopPath, big, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("큰 본문 stop status = %d, want 400", res.StatusCode)
	}
}

// 저장이 실패해도 웹 이벤트는 버퍼에 남아 재업로드에서 병합된다.
func TestHandleRecordingKeepsWebEventsOnSaveFailure(t *testing.T) {
	ts := newWebTestServer(t)
	start := recentWebStartEpochMs()
	ts.post(t, apiWebStartPath, `{"startEpochMs":`+itoa(start)+`}`, nil).Body.Close()
	ts.server.web.Add(webClickPayload(start+1500, "div.card", "https://example.com"))

	// 녹화 디렉토리 자리에 일반 파일을 두어 MkdirAll이 실패하게 한다.
	blocker := filepath.Join(ts.root, recordingsDirName)
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("blocker write error: %v", err)
	}
	upload := func() *http.Response {
		body, contentType := buildRecordingForm(t, "AUDIO", "audio/webm", `{"version":1,"durationMs":5000,"events":[]}`)
		req, _ := http.NewRequest(http.MethodPost, ts.URL+apiRecordingPath, body)
		req.Header.Set("Content-Type", contentType)
		req.Header.Set(originHeader, ts.URL)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("upload error: %v", err)
		}
		return res
	}

	res := upload()
	res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("저장 실패 status = %d, want 500", res.StatusCode)
	}
	if _, count := ts.server.web.Live(); count != 1 {
		t.Fatalf("저장 실패 후 버퍼 = %d개, want 1", count)
	}

	if err := os.Remove(blocker); err != nil {
		t.Fatalf("blocker remove error: %v", err)
	}
	res = upload()
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("재업로드 status = %d, want 200", res.StatusCode)
	}
	result := decodeJSON[RecordingResult](t, res)
	data, err := os.ReadFile(filepath.Join(result.Dir, eventsFileName))
	if err != nil {
		t.Fatalf("events.json 읽기 실패: %v", err)
	}
	var saved RecordingMeta
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("events.json 파싱 실패: %v", err)
	}
	if len(saved.Events) != 1 || saved.Events[0].Type != WebEventClick || saved.Events[0].T != 1500 {
		t.Errorf("재업로드 events = %+v, want 웹 클릭 1개 (t=1500)", saved.Events)
	}
	if _, count := ts.server.web.Live(); count != 0 {
		t.Errorf("저장 성공 후 버퍼에 %d개 남음", count)
	}
}

// DNS 리바인딩: evil.com이 127.0.0.1을 가리키게 된 페이지는 Origin과 Host를 모두 evil.com:<port>로 맞출 수 있다.
// 호스트:포트 일치만으로는 통과하므로 로컬 호스트 이름 조건과 전역 Host 가드가 함께 막아야 한다.
func TestRejectsDNSRebindingHost(t *testing.T) {
	ts := newWebTestServer(t)
	port := ts.Listener.Addr().(*net.TCPAddr).Port
	evilHost := "evil.com:" + strconv.Itoa(port)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+apiWebStartPath, strings.NewReader(`{}`))
	req.Host = evilHost
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(originHeader, "http://"+evilHost)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("start error: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("리바인딩 start status = %d, want 403", res.StatusCode)
	}
	if recording, _ := ts.server.web.Live(); recording {
		t.Error("리바인딩 start가 수집을 시작함")
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
