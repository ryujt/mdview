package mdview

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// E2E 테스트 정책 상수
const (
	e2eChromeEnv   = "MDVIEW_E2E_CHROME"
	e2eWaitTimeout = 10 * time.Second
	// 시스템이 바쁠 때 Chrome은 탭 URL이 바뀐 뒤에도 실제 요청을 보내기까지 10초 넘게 걸리기도 한다.
	e2ePageLoadTimeout = 60 * time.Second
	// 수집 스크립트 주입이 끝나기를 기다리며 클릭을 반복하는 총 시간과 간격 (주입은 기동 직후 비동기로 끝난다)
	e2eCaptureTimeout     = 30 * time.Second
	e2eClickRetryInterval = time.Second
	e2ePollInterval       = 100 * time.Millisecond
	e2eDurationMs         = 5000
	e2eStartLead          = time.Second
	e2eSiteHTML           = `<html><head><title>E2E Site</title></head><body><h2>핵심 지표</h2><p id="metric" class="metric-value">12,345</p></body></html>`
	e2eChromeMissing      = "Chrome을 찾을 수 없습니다"
)

// TestWebCaptureEndToEnd는 실제 Chrome을 띄워 웹 열기 → 실제 클릭 수집 → 업로드 병합 → prompt.txt까지 검증한다.
// 화면에 Chrome 창이 뜨므로 MDVIEW_E2E_CHROME=1일 때만 실행한다.
func TestWebCaptureEndToEnd(t *testing.T) {
	if os.Getenv(e2eChromeEnv) != "1" {
		t.Skip(e2eChromeEnv + "=1일 때만 실행 (실제 Chrome 창이 뜬다)")
	}
	if testing.Short() {
		t.Skip("short 모드에서는 건너뜀")
	}

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, e2eSiteHTML)
	}))
	t.Cleanup(site.Close)
	siteURL := site.URL + "/"

	ts := newWebTestServer(t)
	// t.Cleanup은 역순 실행이므로 이 정리가 서버 종료보다 먼저 돈다. 탭 컨텍스트는 Chrome 종료 뒤에 취소한다
	// (첫 컨텍스트가 아닌 chromedp 컨텍스트를 먼저 취소하면 탭이 닫혀 수집 대상이 사라진다).
	var tabCancel context.CancelFunc
	t.Cleanup(func() {
		ts.server.CloseWeb()
		if tabCancel != nil {
			tabCancel()
		}
	})

	e2eOpenWeb(t, ts, siteURL)

	// 녹음 시작을 1초 앞으로 잡아, 클릭이 업로드 메타의 문서 open 이벤트(t=100)보다 항상 뒤에 오게 한다.
	start := time.Now().Add(-e2eStartLead)
	res := ts.post(t, apiWebStartPath, `{"startEpochMs":`+itoa(start.UnixMilli())+`}`, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d, want 200", res.StatusCode)
	}

	tabCancel = e2eClickUntilCaptured(t, ts, site.URL)

	stop := decodeJSON[map[string]any](t, ts.post(t, apiWebStopPath, `{"durationMs":`+itoa(e2eDurationMs)+`}`, nil))
	if stop["ok"] != true {
		t.Fatalf("stop = %v", stop)
	}

	result := e2eUploadRecording(t, ts, start)

	events := e2eReadEvents(t, result.Dir)
	openIdx, clickIdx := -1, -1
	for i, e := range events {
		switch {
		case e.Type == "open" && openIdx < 0:
			openIdx = i
		case e.Type == WebEventClick && clickIdx < 0:
			clickIdx = i
		}
	}
	if openIdx < 0 || clickIdx < 0 {
		t.Fatalf("open·web-click 이벤트가 모두 있어야 함: %+v", events)
	}
	click := events[clickIdx]
	if !strings.HasPrefix(click.URL, site.URL) {
		t.Errorf("click.url = %q, want prefix %q", click.URL, site.URL)
	}
	if click.Text != "12,345" || click.Tag != "p" || click.Classes != "metric-value" || click.Heading != "핵심 지표" {
		t.Errorf("click = text:%q tag:%q classes:%q heading:%q", click.Text, click.Tag, click.Classes, click.Heading)
	}
	if clickIdx < openIdx || click.T < events[openIdx].T {
		t.Errorf("web-click(t=%d, #%d)이 open(t=%d, #%d)보다 앞섬", click.T, clickIdx, events[openIdx].T, openIdx)
	}

	job := ts.waitForJob(t, result.ID)
	if job.State != TranscribeStateDone {
		t.Fatalf("job state = %q (%s), want done", job.State, job.Error)
	}
	prompt, err := os.ReadFile(filepath.Join(result.Dir, promptFileName))
	if err != nil {
		t.Fatalf("prompt.txt 읽기 실패: %v", err)
	}
	for _, want := range []string{`웹 클릭: "12,345" (p.metric-value)`, `url: "` + site.URL} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("prompt.txt에 %q 없음:\n%s", want, prompt)
		}
	}

	// 재기동: Chrome을 닫은 뒤 다시 열면 새로 기동해야 한다.
	ts.server.CloseWeb()
	e2eOpenWeb(t, ts, siteURL)
}

// e2eOpenWeb은 /api/web/open으로 Chrome을 새로 띄운다. Chrome이 없으면 테스트를 건너뛴다.
func e2eOpenWeb(t *testing.T, ts *webTestServer, siteURL string) {
	t.Helper()
	res := ts.post(t, apiWebOpenPath, `{"url":`+e2eQuote(siteURL)+`}`, nil)
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("open 응답 읽기 실패: %v", err)
	}
	if res.StatusCode == http.StatusBadRequest && strings.Contains(string(raw), e2eChromeMissing) {
		t.Skip(strings.TrimSpace(string(raw)))
	}
	var body map[string]any
	if res.StatusCode != http.StatusOK || json.Unmarshal(raw, &body) != nil || body["launched"] != true {
		t.Fatalf("open status = %d body = %s, want 200 launched:true", res.StatusCode, raw)
	}
}

// e2eClickUntilCaptured는 전용 Chrome의 사이트 탭에서 #metric을 실제 마우스 이벤트로 클릭하고
// /api/web/live가 수집 건수 1 이상과 chrome:true를 보고할 때까지 기다린다.
// 수집 스크립트 주입은 기동 직후 비동기로 끝나므로, 주입 전에 누른 클릭은 유실될 수 있어 수집될 때까지 다시 누른다.
// 반환한 cancel은 테스트가 끝난 뒤(Chrome 종료 후)에만 호출해야 한다.
func e2eClickUntilCaptured(t *testing.T, ts *webTestServer, sitePrefix string) context.CancelFunc {
	t.Helper()
	o := ts.server.webOpener
	o.mu.Lock()
	b := o.browser
	o.mu.Unlock()
	if b == nil {
		t.Fatal("Chrome이 떠 있지 않음")
	}
	rootCtx := b.RootCtx()

	id := e2eWaitSiteTarget(t, rootCtx, sitePrefix)
	tctx, cancel := chromedp.NewContext(rootCtx, chromedp.WithTargetID(id))
	// 첫 Run은 타깃에 attach하면서 넘긴 컨텍스트에 세션 수명을 묶는다. 시한부 자식으로 attach하면
	// 그 자식이 취소되는 순간 이후 Run이 모두 멈추므로, attach는 tctx 자체로 한다 (막히면 테스트 시한이 끊는다).
	if err := chromedp.Run(tctx); err != nil {
		cancel()
		t.Fatalf("사이트 탭 attach 실패: %v", err)
	}
	loadCtx, loadCancel := context.WithTimeout(tctx, e2ePageLoadTimeout)
	err := chromedp.Run(loadCtx, chromedp.WaitVisible("#metric", chromedp.ByID))
	loadCancel()
	if err != nil {
		cancel()
		t.Fatalf("사이트가 %s 안에 로드되지 않음: %v", e2ePageLoadTimeout, err)
	}
	deadline := time.Now().Add(e2eCaptureTimeout)
	clicks := 0
	for {
		// 시한은 클릭마다 별도 자식 컨텍스트로만 건다. tctx 자체를 취소하면 탭이 닫힌다.
		runCtx, runCancel := context.WithTimeout(tctx, e2eWaitTimeout)
		err := chromedp.Run(runCtx, chromedp.Click("#metric", chromedp.ByID))
		runCancel()
		if err != nil {
			cancel()
			t.Fatalf("클릭 실패: %v", err)
		}
		clicks++
		live := e2ePollLive(ts, e2eClickRetryInterval)
		if n, _ := live["events"].(float64); n >= 1 {
			if live["chrome"] != true {
				cancel()
				t.Fatalf("live = %v, want chrome:true", live)
			}
			t.Logf("웹 클릭 수집 확인 (클릭 %d회)", clicks)
			return cancel
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("웹 클릭이 %s 안에 수집되지 않음 (클릭 %d회): live = %v", e2eCaptureTimeout, clicks, live)
		}
	}
}

// e2eWaitSiteTarget은 URL이 sitePrefix로 시작하는 page 타깃이 나타날 때까지 기다린다.
func e2eWaitSiteTarget(t *testing.T, rootCtx context.Context, sitePrefix string) target.ID {
	t.Helper()
	deadline := time.Now().Add(e2eWaitTimeout)
	for {
		infos, err := listTargets(rootCtx)
		if err != nil {
			t.Fatalf("타깃 목록 실패: %v", err)
		}
		for _, info := range infos {
			if info.Type == targetTypePage && strings.HasPrefix(info.URL, sitePrefix) {
				return info.TargetID
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("사이트 탭이 %s 안에 나타나지 않음: %+v", e2eWaitTimeout, infos)
		}
		time.Sleep(e2ePollInterval)
	}
}

// e2ePollLive는 d 동안 /api/web/live를 폴링하다 이벤트가 잡히면 즉시, 아니면 마지막 응답을 돌려준다.
func e2ePollLive(ts *webTestServer, d time.Duration) map[string]any {
	deadline := time.Now().Add(d)
	var live map[string]any
	for {
		res, err := http.Get(ts.URL + apiWebLivePath)
		if err == nil {
			live = nil
			_ = json.NewDecoder(res.Body).Decode(&live)
			res.Body.Close()
			if n, _ := live["events"].(float64); n >= 1 {
				return live
			}
		}
		if time.Now().After(deadline) {
			return live
		}
		time.Sleep(e2ePollInterval)
	}
}

// e2eUploadRecording은 문서 open 이벤트 하나를 가진 녹음을 업로드한다 (서버가 웹 이벤트를 병합한다).
func e2eUploadRecording(t *testing.T, ts *webTestServer, start time.Time) RecordingResult {
	t.Helper()
	meta := map[string]any{
		"version":    1,
		"mode":       "folder",
		"root":       ts.root,
		"startedAt":  start.UTC().Format(time.RFC3339),
		"durationMs": e2eDurationMs,
		"events":     []map[string]any{{"t": 100, "type": "open", "file": "doc.md"}},
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("meta marshal error: %v", err)
	}
	body, contentType := buildRecordingForm(t, "AUDIOBYTES", "audio/webm;codecs=opus", string(metaJSON))
	req, _ := http.NewRequest(http.MethodPost, ts.URL+apiRecordingPath, body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set(originHeader, ts.URL)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload error: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("upload status = %d, want 200", res.StatusCode)
	}
	return decodeJSON[RecordingResult](t, res)
}

func e2eReadEvents(t *testing.T, dir string) []RecordingEvent {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, eventsFileName))
	if err != nil {
		t.Fatalf("events.json 읽기 실패: %v", err)
	}
	var saved RecordingMeta
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("events.json 파싱 실패: %v", err)
	}
	return saved.Events
}

func e2eQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
