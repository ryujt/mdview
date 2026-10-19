package mdview

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/ryujt/mdview/internal/mdview/logging"
)

// Chrome 기동 정책 상수. 전용 프로필을 쓰는 이유: 최신 Chrome은 기본 user-data-dir에 대해
// --remote-debugging-port를 거부하고, 사용자의 일상 프로필을 CDP에 노출시키지 않아야 한다.
// webview와 프로필을 분리해 두 도구를 동시에 실행해도 프로필 락이 겹치지 않게 한다.
const (
	chromeProfileDirName = "chrome-profile"
	browseWindowSize     = "1280,900"
	// 첫 탭 이동 명령(Page.navigate) 응답 대기 상한. 로드 완료가 아니라 명령 응답만 기다린다.
	navigateTimeout = 10 * time.Second
)

// WebBrowser는 headful Chrome 인스턴스를 소유하는 Gateway이다.
type WebBrowser struct {
	allocCancel context.CancelFunc
	rootCtx     context.Context
	rootCancel  context.CancelFunc
}

// LaunchWebBrowser는 전용 프로필로 Chrome을 띄우고 첫 탭에서 startURL을 연다.
// 반환된 RootCtx는 브라우저 수명에 묶여 있으며 탭이 닫혀도 취소되지 않는다 (Chrome 종료 시에만 취소).
func LaunchWebBrowser(startURL string) (*WebBrowser, error) {
	chromePath := findChrome()
	if chromePath == "" {
		return nil, ErrChromeUnavailable
	}
	profileDir, err := chromeProfileDir()
	if err != nil {
		return nil, err
	}
	logging.Info("[WebBrowser] Chrome 실행: %s (프로필 %s)", chromePath, profileDir)

	// DefaultExecAllocatorOptions는 헤드리스 렌더링용 플래그를 담고 있어 쓰지 않는다. --headless를 넘기지 않으므로 headful로 뜬다.
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(),
		chromedp.ExecPath(chromePath),
		chromedp.UserDataDir(profileDir),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("hide-crash-restore-bubble", true),
		// macOS 키체인 접근 프롬프트("Chrome Safe Storage")를 띄우지 않는다. 전용 저신뢰 프로필이라
		// 쿠키 암호화 키를 고정 키로 대체해도 되고, 사용자 키체인을 건드리지 않는 쪽이 안전하다.
		chromedp.Flag("use-mock-keychain", true),
		chromedp.Flag("window-size", browseWindowSize),
	)
	rootCtx, rootCancel := chromedp.NewContext(allocCtx)
	b := &WebBrowser{allocCancel: allocCancel, rootCtx: rootCtx, rootCancel: rootCancel}

	// 먼저 Chrome 기동과 첫 탭 attach만 확인한다. 프로필 락 안내는 이 실패에만 해당한다.
	if err := chromedp.Run(rootCtx); err != nil {
		b.Close()
		return nil, fmt.Errorf("Chrome 기동 실패 (같은 프로필로 mdview가 이미 실행 중인지 확인하세요): %w", err)
	}
	// 로드 완료를 기다리지 않고 이동만 지시한다. 사용자가 입력한 URL은 오타·연결 거부·느린 사이트일 수 있고,
	// 그 경우 Chrome 자체의 오류 페이지를 보여 주는 것이 맞다. 수집 스크립트는 RunImmediately로 진행 중인 문서에도 적용된다.
	if err := navigateWithoutWait(rootCtx, startURL); err != nil {
		b.Close()
		return nil, fmt.Errorf("첫 탭 이동 실패: %w", err)
	}
	logging.Info("[WebBrowser] 준비 완료: %s", startURL)
	return b, nil
}

// navigateWithoutWait는 현재 탭에 이동을 지시하되 로드 이벤트를 기다리지 않는다.
// 페이지 수준 오류(errorText)는 Chrome이 오류 페이지로 보여 주므로 경고만 남긴다.
// Page.navigate 응답 자체가 navigateTimeout 안에 오지 않으면(응답 없는 서버 등) 경고만 남기고 성공으로 본다 —
// Chrome은 떠 있고 탭은 계속 로딩한다. 호출자가 WebOpener.mu를 잡고 있으므로 무기한 대기하면 안 된다.
// 시한부 컨텍스트는 이 Run에만 쓰이고 루트 컨텍스트(브라우저 수명)는 취소하지 않는다.
func navigateWithoutWait(ctx context.Context, url string) error {
	runCtx, cancel := context.WithTimeout(ctx, navigateTimeout)
	defer cancel()
	err := chromedp.Run(runCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, _, errorText, _, err := page.Navigate(url).Do(ctx)
		if err != nil {
			return err
		}
		if errorText != "" {
			logging.Warn("[WebBrowser] 첫 페이지 이동 오류 (Chrome 오류 페이지 표시): %s — %s", url, errorText)
		}
		return nil
	}))
	if errors.Is(err, context.DeadlineExceeded) {
		logging.Warn("[WebBrowser] 첫 페이지 이동 응답이 %s 안에 오지 않음 — 탭은 계속 로딩합니다: %s", navigateTimeout, url)
		return nil
	}
	return err
}

// RootCtx는 브라우저 수명 컨텍스트를 반환한다 (WebCapture가 타깃별 자식 컨텍스트를 파생시킨다).
func (b *WebBrowser) RootCtx() context.Context {
	return b.rootCtx
}

// OpenTab은 같은 창에 새 탭으로 url을 연다.
func (b *WebBrowser) OpenTab(url string) error {
	return runBrowserAction(b.rootCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := target.CreateTarget(url).Do(ctx)
		return err
	}))
}

// Done은 Chrome 프로세스가 종료되면 닫히는 채널을 반환한다.
func (b *WebBrowser) Done() <-chan struct{} {
	return b.rootCtx.Done()
}

// Close는 Chrome을 종료한다 (컨텍스트 취소 → allocator 정리 순서).
func (b *WebBrowser) Close() {
	if b.rootCancel != nil {
		b.rootCancel()
	}
	if b.allocCancel != nil {
		b.allocCancel()
	}
}

// runBrowserAction은 브라우저 도메인 명령을 특정 탭과 무관하게 실행한다.
func runBrowserAction(ctx context.Context, actions ...chromedp.Action) error {
	c := chromedp.FromContext(ctx)
	if c == nil || c.Browser == nil {
		return chromedp.ErrInvalidContext
	}
	return chromedp.Tasks(actions).Do(cdp.WithExecutor(ctx, c.Browser))
}

func chromeProfileDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, chromeProfileDirName), nil
}
