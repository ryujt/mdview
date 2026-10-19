package mdview

import (
	"errors"
	"os"
)

// chromePathEnv는 Chrome 실행 파일을 직접 지정하는 환경변수이다.
const chromePathEnv = "MDVIEW_CHROME_PATH"

// ErrChromeUnavailable은 Chrome / Chromium을 찾을 수 없을 때 반환된다.
var ErrChromeUnavailable = errors.New(
	"Chrome을 찾을 수 없습니다. Google Chrome을 설치하거나 " + chromePathEnv + " 환경변수로 실행 파일 경로를 지정하세요")

// chromeCandidates는 OS별 Chrome / Chromium 표준 설치 경로이다.
// mdview는 사용자가 실제로 로그인해 웹을 탐색하는 브라우저를 띄우므로
// 헤드리스 전용 번들이 아니라 시스템에 설치된 Chrome을 쓴다.
var chromeCandidates = []string{
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
	"/usr/bin/google-chrome",
	"/usr/bin/google-chrome-stable",
	"/usr/bin/chromium",
	"/usr/bin/chromium-browser",
	"/snap/bin/chromium",
	`C:\Program Files\Google\Chrome\Application\chrome.exe`,
	`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
}

// findChrome은 Chrome 실행 경로를 환경변수 → 시스템 표준 경로 순으로 탐색한다.
// 모두 실패하면 빈 문자열을 반환한다.
func findChrome() string {
	if p := os.Getenv(chromePathEnv); p != "" {
		if isExecutableFile(p) {
			return p
		}
	}
	for _, p := range chromeCandidates {
		if isExecutableFile(p) {
			return p
		}
	}
	return ""
}

func isExecutableFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
