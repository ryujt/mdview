package mdview

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ryujt/mdview/internal/mdview/logging"
)

// ErrInvalidWebURL은 http/https가 아니거나 호스트가 없거나 사용자 정보(user:pw@)가 담긴 URL에 반환된다.
var ErrInvalidWebURL = errors.New("http/https URL만 열 수 있습니다")

// ValidateWebURL은 사용자가 입력한 URL을 검증하고 정규화한 문자열을 반환한다.
// 사용자 정보가 있는 URL은 거부한다 (자격 증명이 로그·프롬프트에 남지 않도록).
// 호스트 이름이 비어 있는 URL(http://:80)도 거부한다.
func ValidateWebURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return "", ErrInvalidWebURL
	}
	return u.String(), nil
}

// WebOpener는 전용 Chrome의 수명을 소유한다. 첫 열기에서 기동하고, 이후에는 같은 창에 탭을 연다.
// 모든 창이 닫히거나 Chrome이 죽으면 정리하고 다음 열기에서 다시 기동한다.
type WebOpener struct {
	mu      sync.Mutex
	browser *WebBrowser
	capture *WebCapture
	onEvent func(payload string)
	// alive는 Alive()가 o.mu 없이 읽는 상태이다. Open이 기동·이동 중 o.mu를 오래 잡아도 /api/web/live가 막히지 않는다.
	alive atomic.Bool
	// launch는 테스트에서 교체하기 위한 기동 함수이다.
	launch func(url string) (*WebBrowser, error)
	// attach는 기동한 브라우저에 이벤트 수집을 붙이는 함수이다 (테스트에서 교체).
	attach func(b *WebBrowser) (*WebCapture, error)
}

// NewWebOpener는 수집한 이벤트 payload를 onEvent로 보고하는 WebOpener를 만든다.
func NewWebOpener(onEvent func(string)) *WebOpener {
	o := &WebOpener{onEvent: onEvent, launch: LaunchWebBrowser}
	o.attach = o.attachCapture
	return o
}

// attachCapture는 기본 attach 구현이다. 모든 page 타깃이 닫히면 해당 브라우저를 정리한다.
func (o *WebOpener) attachCapture(b *WebBrowser) (*WebCapture, error) {
	capture := NewWebCapture(b.RootCtx())
	capture.OnEvent = o.onEvent
	capture.OnAllTargetsClosed = func() { o.release(b) }
	if err := capture.Start(); err != nil {
		return nil, err
	}
	return capture, nil
}

// Open은 URL을 Chrome에서 연다. 새로 기동했으면 launched=true.
// 기존 브라우저는 수집기가 살아 있고(모든 탭이 닫히지 않았고) 새 탭 열기가 성공할 때만 재사용한다.
func (o *WebOpener) Open(raw string) (launched bool, err error) {
	target, err := ValidateWebURL(raw)
	if err != nil {
		return false, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.browser != nil {
		if o.capture != nil && !o.capture.Closed() {
			err := o.browser.OpenTab(target)
			if err == nil {
				return false, nil
			}
			logging.Warn("[WebOpener] 탭 열기 실패 — Chrome을 다시 기동합니다: %v", err)
		} else {
			logging.Info("[WebOpener] 이전 Chrome의 탭이 모두 닫힘 — 다시 기동합니다")
		}
		o.dropLocked()
	}
	b, err := o.launch(target)
	if err != nil {
		return false, err
	}
	capture, err := o.attach(b)
	if err != nil {
		b.Close()
		return false, fmt.Errorf("이벤트 수집 시작 실패: %w", err)
	}
	o.browser = b
	o.capture = capture
	o.alive.Store(true)
	if b.RootCtx() != nil {
		go func() {
			<-b.Done()
			o.release(b)
		}()
	}
	return true, nil
}

// dropLocked는 현재 브라우저를 닫고 상태를 비운다. 반드시 o.mu를 잡은 상태로 호출한다.
func (o *WebOpener) dropLocked() {
	if o.browser != nil {
		o.browser.Close()
	}
	o.browser = nil
	o.capture = nil
	o.alive.Store(false)
}

// release는 주어진 인스턴스가 아직 현재 브라우저일 때만 정리한다 (뒤늦은 콜백이 새 인스턴스를 닫지 않도록).
func (o *WebOpener) release(b *WebBrowser) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.browser != b {
		return
	}
	o.dropLocked()
	logging.Info("[WebOpener] Chrome 정리됨")
}

// Alive는 Chrome이 떠 있는지 반환한다. 락을 잡지 않으므로 기동 중에도 즉시 답한다.
func (o *WebOpener) Alive() bool {
	return o.alive.Load()
}

// Close는 Chrome을 종료한다 (mdview 종료 경로). 여러 번 불러도 안전하다.
func (o *WebOpener) Close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.dropLocked()
}
