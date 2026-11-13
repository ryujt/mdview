package mdview

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// Headless, isolated profile and HOME. No microphone or paid API calls are made.
func TestSettingsBrowserEndToEnd(t *testing.T) {
	if os.Getenv("MDVIEW_E2E_SETTINGS") != "1" {
		t.Skip("MDVIEW_E2E_SETTINGS=1 enables headless browser checks")
	}
	chrome := findChrome()
	if chrome == "" {
		t.Fatal("Chrome is required for the requested browser check")
	}
	for _, mode := range []Mode{ModeFolder, ModeFile} {
		t.Run(string(mode), func(t *testing.T) {
			setTestHome(t, t.TempDir())
			t.Setenv(openaiKeyEnv, "")
			root := t.TempDir()
			file := filepath.Join(root, "document.md")
			if err := os.WriteFile(file, []byte("# Settings browser check\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			hub := NewHub()
			go hub.Run()
			var server *Server
			var err error
			if mode == ModeFile {
				server, err = NewFileServer(hub, file)
			} else {
				server, err = NewFolderServer(hub, root)
			}
			if err != nil {
				t.Fatal(err)
			}
			ts := httptest.NewServer(server.Handler())
			defer ts.Close()
			options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chrome), chromedp.UserDataDir(t.TempDir()), chromedp.Flag("use-mock-keychain", true))
			allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
			defer cancelAllocator()
			browser, cancelBrowser := chromedp.NewContext(allocator)
			defer cancelBrowser()
			ctx, cancel := context.WithTimeout(browser, 60*time.Second)
			defer cancel()
			run := func(actions ...chromedp.Action) {
				t.Helper()
				if err := chromedp.Run(ctx, actions...); err != nil {
					t.Fatal(err)
				}
			}
			open := func() {
				t.Helper()
				run(chromedp.Click("#settings-button", chromedp.ByQuery), chromedp.Poll(`document.querySelector('#settings-dialog').open && !document.querySelector('#settings-fields').disabled`, nil))
			}
			run(chromedp.EmulateViewport(1120, 780), chromedp.Navigate(ts.URL), chromedp.WaitVisible("#settings-button", chromedp.ByQuery))
			var positionOK bool
			run(chromedp.Evaluate(`(()=>{const r=document.querySelector('#settings-button').getBoundingClientRect(); return r.left===16 && innerHeight-r.bottom===16 && r.width===44})()`, &positionOK))
			if !positionOK {
				t.Fatal("settings button is not fixed at the bottom left")
			}
			open()
			run(chromedp.Poll(`document.querySelector('#settings-model').value === 'whisper-1'`, nil))
			const secret = "sk-settings-ui-test-not-a-real-key"
			run(chromedp.SendKeys("#settings-api-key", secret, chromedp.ByQuery), chromedp.SetValue("#settings-model", "gpt-4o-mini-transcribe", chromedp.ByQuery), chromedp.Click("#settings-save", chromedp.ByQuery), chromedp.Poll(`document.querySelector('#settings-message').textContent.includes('Saved')`, nil))
			cfg, err := LoadConfig()
			if err != nil || cfg.OpenAIAPIKey != secret || cfg.TranscribeModel != "gpt-4o-mini-transcribe" {
				t.Fatal("browser settings were not persisted")
			}
			var cleared bool
			run(chromedp.Evaluate(`document.querySelector('#settings-api-key').value==='' && document.querySelector('#settings-api-key').type==='password'`, &cleared))
			if !cleared {
				t.Fatal("password input was not cleared after saving")
			}
			run(chromedp.Click("#settings-close", chromedp.ByQuery), chromedp.Reload(), chromedp.WaitVisible("#settings-button", chromedp.ByQuery))
			open()
			run(chromedp.Poll(`document.querySelector('#settings-model').value==='gpt-4o-mini-transcribe' && document.querySelector('#settings-api-key').value==='' && document.querySelector('#settings-key-badge').textContent==='Set'`, nil))
			var storage string
			run(chromedp.Evaluate(`JSON.stringify({local:{...localStorage},session:{...sessionStorage}})`, &storage))
			if strings.Contains(storage, secret) {
				t.Fatal("API key was written to browser storage")
			}
			// Cancel discards key edits; the modal also clears the input on Escape.
			run(chromedp.SendKeys("#settings-api-key", "do-not-save", chromedp.ByQuery), chromedp.KeyEvent(kb.Escape), chromedp.Poll(`!document.querySelector('#settings-dialog').open && document.querySelector('#settings-api-key').value===''`, nil))
			cfg, _ = LoadConfig()
			if cfg.OpenAIAPIKey != secret {
				t.Fatal("closing without saving changed the stored key")
			}
			open()
			run(chromedp.Click("#settings-key-clear", chromedp.ByQuery), chromedp.Click("#settings-save", chromedp.ByQuery), chromedp.Poll(`document.querySelector('#settings-message').textContent.includes('Saved') && document.querySelector('#settings-key-badge').textContent==='Not set'`, nil))
			cfg, _ = LoadConfig()
			if cfg.OpenAIAPIKey != "" {
				t.Fatal("key deletion did not persist")
			}
			run(chromedp.EmulateViewport(390, 740))
			var fits bool
			run(chromedp.Evaluate(`(()=>{const r=document.querySelector('#settings-dialog').getBoundingClientRect();return r.left>=0 && r.right<=innerWidth && r.top>=0 && r.bottom<=innerHeight})()`, &fits))
			if !fits {
				t.Fatal("settings dialog overflows a narrow viewport")
			}
			if screenshotDir := os.Getenv("MDVIEW_SETTINGS_SCREENSHOTS"); screenshotDir != "" {
				if err := os.MkdirAll(screenshotDir, 0o755); err != nil {
					t.Fatal(err)
				}
				var screenshot []byte
				run(chromedp.EmulateViewport(1120, 780), chromedp.CaptureScreenshot(&screenshot))
				if err := os.WriteFile(filepath.Join(screenshotDir, "settings-"+string(mode)+".png"), screenshot, 0o644); err != nil {
					t.Fatal(err)
				}
				metadata, _ := json.Marshal(map[string]string{"mode": string(mode), "status": "passed"})
				if err := os.WriteFile(filepath.Join(screenshotDir, "settings-"+string(mode)+".json"), metadata, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// e2eChromeCandidates are the standard Chrome / Chromium install paths the browser E2E test looks for.
var e2eChromeCandidates = []string{
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
	"/usr/bin/google-chrome",
	"/usr/bin/google-chrome-stable",
	"/usr/bin/chromium",
	"/usr/bin/chromium-browser",
	`C:\Program Files\Google\Chrome\Application\chrome.exe`,
	`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
}

// findChrome finds the installed Chrome executable path. It returns an empty string if none is found.
func findChrome() string {
	for _, p := range e2eChromeCandidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}
