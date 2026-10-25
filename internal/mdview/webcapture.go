package mdview

import (
	"context"
	_ "embed"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/ryu/tools-camp/cli/mdview/internal/mdview/logging"
)

//go:embed inject/capture.js
var captureJS string

// 이벤트 수집 주입 정책 상수
const (
	// bindingName은 격리 월드에 심는 네이티브 함수 이름이다. CDP binding이라 대상 페이지의 CSP·CORS에 걸리지 않는다.
	bindingName = "__mdviewCaptureEvent__"
	// isolatedWorldName은 캡처 스크립트가 사는 격리 월드 이름이다. 페이지 JS는 이 월드를 볼 수 없어 이벤트를 위조할 수 없다.
	isolatedWorldName = "__mdview__"
	// 수집 대상 타깃 종류. iframe·worker는 대상이 아니다.
	targetTypePage = "page"
	// 타깃 이벤트 큐 크기. 넘치면 goroutine으로 넘겨 유실 없이 처리한다.
	targetEventBuffer = 256
	// 타깃 한 개의 주입에 허용하는 시간. 응답 없는 페이지가 타깃 처리 전체를 막지 못하게 한다.
	injectTimeout = 10 * time.Second
)

// WebCapture는 Chrome의 모든 page 타깃에 캡처 스크립트를 주입하고 이벤트를 수신하는 Worker이다.
// 상위를 직접 참조하지 않고 콜백으로만 보고한다.
type WebCapture struct {
	// OnEvent는 검증 전 원문 payload를 넘긴다 (정본 검증은 WebSession이 한다).
	OnEvent func(payload string)
	// OnAllTargetsClosed는 page 타깃이 하나도 남지 않았을 때 한 번 호출된다.
	OnAllTargetsClosed func()

	rootCtx context.Context

	mu     sync.Mutex
	pages  map[target.ID]context.CancelFunc
	closed bool
}

type targetEvent struct {
	id      target.ID
	created bool
}

// NewWebCapture는 브라우저 수명 컨텍스트에 붙는 WebCapture를 만든다.
func NewWebCapture(rootCtx context.Context) *WebCapture {
	return &WebCapture{rootCtx: rootCtx, pages: map[target.ID]context.CancelFunc{}}
}

// Start는 타깃 추적을 시작한다. 리스너를 먼저 걸어야 그 사이 열린 탭이 누락되지 않고,
// 열거 결과와 리스너 결과를 같은 맵에 통과시켜야 중복 주입으로 이벤트가 2배가 되지 않는다.
func (c *WebCapture) Start() error {
	events := make(chan targetEvent, targetEventBuffer)
	send := func(ev targetEvent) {
		select {
		case events <- ev:
		default:
			go func() {
				select {
				case events <- ev:
				case <-c.rootCtx.Done():
				}
			}()
		}
	}
	chromedp.ListenBrowser(c.rootCtx, func(ev any) {
		switch e := ev.(type) {
		case *target.EventTargetCreated:
			if e.TargetInfo.Type == targetTypePage {
				send(targetEvent{id: e.TargetInfo.TargetID, created: true})
			}
		case *target.EventTargetDestroyed:
			send(targetEvent{id: e.TargetID})
		}
	})
	go c.processTargets(events)

	if err := runBrowserAction(c.rootCtx, target.SetDiscoverTargets(true)); err != nil {
		return err
	}
	infos, err := listTargets(c.rootCtx)
	if err != nil {
		return err
	}
	for _, info := range infos {
		if info.Type == targetTypePage {
			send(targetEvent{id: info.TargetID, created: true})
		}
	}
	return nil
}

// Closed는 모든 page 타깃이 닫혀 수집기가 끝났는지 반환한다. 끝난 수집기의 브라우저는 재사용하지 않는다.
func (c *WebCapture) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *WebCapture) processTargets(events <-chan targetEvent) {
	for {
		select {
		case <-c.rootCtx.Done():
			return
		case ev := <-events:
			if ev.created {
				c.addTarget(ev.id)
			} else {
				c.removeTarget(ev.id)
			}
		}
	}
}

func (c *WebCapture) addTarget(id target.ID) {
	c.mu.Lock()
	if _, known := c.pages[id]; known {
		c.mu.Unlock()
		return
	}
	c.pages[id] = nil
	c.mu.Unlock()

	cancel, err := c.inject(id)
	if err != nil {
		logging.Warn("[WebCapture] 타깃 주입 실패 (%s): %v", id, err)
		c.mu.Lock()
		empty := c.forgetTargetLocked(id)
		c.mu.Unlock()
		c.notifyIfEmpty(empty)
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		return
	}
	if _, alive := c.pages[id]; !alive {
		c.mu.Unlock()
		cancel()
		return
	}
	c.pages[id] = cancel
	c.mu.Unlock()
	logging.Info("[WebCapture] 타깃 주입 완료: %s", id)
}

func (c *WebCapture) removeTarget(id target.ID) {
	c.mu.Lock()
	cancel, known := c.pages[id]
	if !known {
		c.mu.Unlock()
		return
	}
	empty := c.forgetTargetLocked(id)
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	c.notifyIfEmpty(empty)
}

// forgetTargetLocked는 타깃을 맵에서 제거하고, 마지막 타깃이었다면 closed를 세우고 true를 반환한다.
// 반드시 c.mu를 잡은 상태로 호출한다. 콜백은 호출하지 않는다 (락 밖에서 notifyIfEmpty로 호출).
func (c *WebCapture) forgetTargetLocked(id target.ID) (empty bool) {
	delete(c.pages, id)
	empty = len(c.pages) == 0 && !c.closed
	if empty {
		c.closed = true
	}
	return empty
}

func (c *WebCapture) notifyIfEmpty(empty bool) {
	if empty && c.OnAllTargetsClosed != nil {
		logging.Info("[WebCapture] 모든 page 타깃이 닫힘")
		c.OnAllTargetsClosed()
	}
}

// inject는 타깃에 binding과 캡처 스크립트를 격리 월드로 심고 이벤트 수신을 시작한다.
// 주입을 별도 goroutine에 맡기고 대기 시간만 제한한다 (alert 등으로 멈춘 페이지가 전체를 막지 않도록).
// ctx 자체를 시한부로 만들면 안 된다 — 취소하는 순간 이후 bindingCalled가 오지 않는다.
func (c *WebCapture) inject(id target.ID) (context.CancelFunc, error) {
	ctx, cancel := chromedp.NewContext(c.rootCtx, chromedp.WithTargetID(id))
	chromedp.ListenTarget(ctx, func(ev any) {
		called, ok := ev.(*runtime.EventBindingCalled)
		if !ok || called.Name != bindingName {
			return
		}
		if c.OnEvent != nil {
			c.OnEvent(called.Payload)
		}
	})
	done := make(chan error, 1)
	go func() {
		done <- chromedp.Run(ctx,
			runtime.AddBinding(bindingName).WithExecutionContextName(isolatedWorldName),
			chromedp.ActionFunc(func(ctx context.Context) error {
				_, err := page.AddScriptToEvaluateOnNewDocument(captureJS).
					WithWorldName(isolatedWorldName).
					WithRunImmediately(true).
					Do(ctx)
				return err
			}),
		)
	}()
	select {
	case err := <-done:
		if err != nil {
			// 주의: 첫 컨텍스트가 아닌 chromedp 컨텍스트의 cancel은 DetachFromTarget 뒤 CloseTarget까지 수행한다. 주입 실패한 탭이 닫힐 수 있다(webview와 동일한 알려진 제한).
			cancel()
			return nil, err
		}
		return cancel, nil
	case <-time.After(injectTimeout):
		logging.Warn("[WebCapture] 타깃 주입이 %s 안에 끝나지 않아 대기를 중단합니다 (%s)", injectTimeout, id)
		return cancel, nil
	}
}

func listTargets(ctx context.Context) ([]*target.Info, error) {
	var infos []*target.Info
	err := runBrowserAction(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		infos, err = target.GetTargets().Do(ctx)
		return err
	}))
	return infos, err
}
