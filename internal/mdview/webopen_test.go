package mdview

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
)

func TestValidateWebURL(t *testing.T) {
	ok := map[string]string{
		"https://example.com/a?b=1": "https://example.com/a?b=1",
		"  http://localhost:3000 ":  "http://localhost:3000",
		"http://[::1]:3000":         "http://[::1]:3000",
	}
	for in, want := range ok {
		got, err := ValidateWebURL(in)
		if err != nil || got != want {
			t.Errorf("ValidateWebURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{
		"", "example.com", "file:///etc/passwd", "javascript:alert(1)", "ftp://x", "https://", "::bad",
		"https://user:pw@h.com", "https://user@h.com", "http://:80",
	} {
		if _, err := ValidateWebURL(bad); !errors.Is(err, ErrInvalidWebURL) {
			t.Errorf("ValidateWebURL(%q) err = %v, want ErrInvalidWebURL", bad, err)
		}
	}
}

func TestWebOpenerPropagatesLaunchError(t *testing.T) {
	o := NewWebOpener(func(string) {})
	o.launch = func(string) (*WebBrowser, error) { return nil, ErrChromeUnavailable }
	launched, err := o.Open("https://example.com")
	if launched || !errors.Is(err, ErrChromeUnavailable) {
		t.Errorf("Open = %v, %v; want false, ErrChromeUnavailable", launched, err)
	}
	if o.Alive() {
		t.Error("기동 실패 후 Alive가 true")
	}
	if _, err := o.Open("not a url"); !errors.Is(err, ErrInvalidWebURL) {
		t.Errorf("잘못된 URL이 launch 전에 거부되지 않음: %v", err)
	}
}

// fakeWebOpener는 실제 Chrome 없이 WebOpener의 수명 관리만 검증한다.
// 더미 WebBrowser는 chromedp 컨텍스트가 아니므로 OpenTab이 항상 chromedp.ErrInvalidContext로 실패한다.
type fakeWebOpener struct {
	*WebOpener
	browsers []*WebBrowser
	captures []*WebCapture
}

func newFakeWebOpener(t *testing.T) *fakeWebOpener {
	t.Helper()
	f := &fakeWebOpener{WebOpener: NewWebOpener(func(string) {})}
	f.launch = func(string) (*WebBrowser, error) {
		ctx, cancel := context.WithCancel(context.Background())
		b := &WebBrowser{rootCtx: ctx, rootCancel: cancel}
		f.browsers = append(f.browsers, b)
		return b, nil
	}
	f.attach = func(*WebBrowser) (*WebCapture, error) {
		c := &WebCapture{pages: map[target.ID]context.CancelFunc{}}
		f.captures = append(f.captures, c)
		return c, nil
	}
	t.Cleanup(f.Close)
	return f
}

func (f *fakeWebOpener) mustOpen(t *testing.T) bool {
	t.Helper()
	launched, err := f.Open("https://example.com")
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	return launched
}

func TestWebOpenerFirstOpenLaunches(t *testing.T) {
	f := newFakeWebOpener(t)
	if !f.mustOpen(t) {
		t.Error("첫 Open이 launched=false")
	}
	if !f.Alive() {
		t.Error("기동 후 Alive가 false")
	}
	if len(f.browsers) != 1 {
		t.Errorf("launch 호출 %d회, want 1", len(f.browsers))
	}
}

// 수집기가 살아 있으면 기존 브라우저에 탭 열기를 시도한다. 더미 브라우저는 OpenTab이 실패하므로
// (실 Chrome이 죽은 경우와 같은 경로) 이전 브라우저를 닫고 다시 기동해야 한다.
func TestWebOpenerRelaunchesWhenOpenTabFails(t *testing.T) {
	f := newFakeWebOpener(t)
	f.mustOpen(t)
	if !f.mustOpen(t) {
		t.Error("OpenTab 실패 후 Open이 launched=false")
	}
	if len(f.browsers) != 2 {
		t.Fatalf("launch 호출 %d회, want 2", len(f.browsers))
	}
	if f.browsers[0].rootCtx.Err() == nil {
		t.Error("재기동 시 이전 브라우저가 닫히지 않음")
	}
	if !f.Alive() {
		t.Error("재기동 후 Alive가 false")
	}
}

// 뒤늦게 도착한 이전 브라우저의 정리 콜백은 새 브라우저를 건드리지 않는다.
func TestWebOpenerStaleReleaseKeepsNewBrowser(t *testing.T) {
	f := newFakeWebOpener(t)
	f.mustOpen(t)
	f.mustOpen(t)
	old, cur := f.browsers[0], f.browsers[1]
	f.release(old)
	if !f.Alive() {
		t.Error("이전 브라우저 release가 Alive를 false로 만듦")
	}
	if cur.rootCtx.Err() != nil {
		t.Error("이전 브라우저 release가 새 브라우저를 닫음")
	}
	f.release(cur)
	if f.Alive() {
		t.Error("현재 브라우저 release 후 Alive가 true")
	}
}

// 모든 탭이 닫혀 수집기가 끝났는데 release가 아직 오지 않은 순간에도, 끝난 브라우저를 재사용하지 않고 다시 기동한다.
func TestWebOpenerRelaunchesWhenCaptureClosed(t *testing.T) {
	f := newFakeWebOpener(t)
	f.mustOpen(t)
	f.captures[0].mu.Lock()
	f.captures[0].closed = true
	f.captures[0].mu.Unlock()
	if !f.captures[0].Closed() {
		t.Fatal("Closed()가 false")
	}
	if !f.mustOpen(t) {
		t.Error("수집기 종료 후 Open이 launched=false")
	}
	if len(f.browsers) != 2 {
		t.Errorf("launch 호출 %d회, want 2", len(f.browsers))
	}
	if f.browsers[0].rootCtx.Err() == nil {
		t.Error("끝난 브라우저가 닫히지 않음")
	}
}

func TestWebOpenerCloseTwiceIsSafe(t *testing.T) {
	f := newFakeWebOpener(t)
	f.mustOpen(t)
	f.Close()
	f.Close()
	if f.Alive() {
		t.Error("Close 후 Alive가 true")
	}
	if f.browsers[0].rootCtx.Err() == nil {
		t.Error("Close가 브라우저를 닫지 않음")
	}
}

// Chrome 프로세스가 죽으면(루트 컨텍스트 취소) 감시 goroutine이 정리한다.
func TestWebOpenerReleasesWhenBrowserDone(t *testing.T) {
	f := newFakeWebOpener(t)
	f.mustOpen(t)
	f.browsers[0].rootCancel()
	for i := 0; i < 200 && f.Alive(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if f.Alive() {
		t.Error("브라우저 종료 후 Alive가 true")
	}
}

func TestWebOpenerAttachErrorClosesBrowser(t *testing.T) {
	f := newFakeWebOpener(t)
	f.attach = func(*WebBrowser) (*WebCapture, error) { return nil, errors.New("boom") }
	if _, err := f.Open("https://example.com"); err == nil {
		t.Fatal("attach 실패가 전파되지 않음")
	}
	if f.Alive() {
		t.Error("attach 실패 후 Alive가 true")
	}
	if f.browsers[0].rootCtx.Err() == nil {
		t.Error("attach 실패 시 브라우저가 닫히지 않음")
	}
}
