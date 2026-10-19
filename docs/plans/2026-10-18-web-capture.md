# mdview 웹 페이지 녹화 통합 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** mdview 툴바에서 외부 웹 사이트를 전용 Chrome 창으로 열고, 녹음 중 그 페이지의 클릭·더블클릭·복사를 문서 이벤트와 같은 타임라인에 기록한 뒤 `transcript.json`과 `prompt.txt`를 만든다.

**Architecture:** `cli/webview`의 Chrome 제어(chromedp)·격리 월드 주입·이벤트 검증·프롬프트 생성 코드를 `cli/mdview/internal/mdview`에 복사해 붙인다. 마이크 녹음은 기존대로 mdview 탭이 하고, 웹 이벤트는 CDP binding → `WebSession`(Go)에 모였다가 녹화 업로드 시 meta.events에 병합된다. 업로드 후에는 항상 `Pipeline`(전사 가능 시 전사 → prompt.txt)이 돈다.

**Tech Stack:** Go 1.26 toolchain(cdproto 요구), chromedp v0.16.0, cdproto v0.0.0-20260804232424-e85f50dbfd32, 기존 net/http·gorilla/websocket, 바닐라 ES module 프론트엔드.

**Spec:** `cli/mdview/docs/specs/2026-10-18-web-capture-design.md`

**작업 규칙:** 모든 명령은 `cli/mdview`에서 실행한다. 커밋 메시지는 한국어 conventional commit(`feat(mdview): …`)이고 끝에 `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` 줄을 둔다. 코드 주석은 기존 파일 수준(패키지·타입·함수 설명 한국어)만 쓴다.

---

## 파일 구조

| 파일 | 상태 | 책임 |
|---|---|---|
| `go.mod`, `go.sum` | 수정 | Go 1.26 + chromedp |
| `internal/mdview/types.go` | 수정 | 웹 이벤트 타입 상수, `TranscribeJob.Prompt/PromptRel` |
| `internal/mdview/transcribe.go` | 수정 | `RecordingEvent` 웹 필드, `RecordingMeta.DroppedWebEvents`, action 매핑·guide, `TranscribeSkipReason` |
| `internal/mdview/websession.go` (+test) | 신규 | 웹 이벤트 검증·버퍼 Service |
| `internal/mdview/prompt.go` (+test) | 신규 | `prompt.txt` 생성 순수 함수 |
| `internal/mdview/pipeline.go` (+test) | 신규 | 전사(가능 시) → prompt.txt Sub-Orchestrator |
| `internal/mdview/recording.go` | 수정 | `mergeWebEvents` (meta 병합·정렬) |
| `internal/mdview/server.go` (+`server_web_test.go`) | 수정 | `/api/web/*`, 업로드 병합, 파이프라인 시작, `CloseWeb`/`WaitPipelines` |
| `internal/mdview/recording_test.go` | 수정 | 기존 업로드 테스트에 키 격리·대기 추가 |
| `internal/mdview/chrome.go` | 신규 | Chrome 실행 파일 탐색 |
| `internal/mdview/webbrowser.go` | 신규 | 전용 프로필 Chrome 기동·탭 열기 Gateway |
| `internal/mdview/webcapture.go` | 신규 | 타깃 추적·주입·binding 수신 Worker |
| `internal/mdview/inject/capture.js` | 신규 | 주입 스크립트 |
| `internal/mdview/webopen.go` (+test) | 신규 | URL 검증 + Chrome 수명 소유(`WebOpener`) |
| `internal/mdview/config.go` | 수정 | `ConfigDir()` |
| `internal/mdview/static/index.html`, `css/style.css` | 수정 | 툴바 URL 입력·버튼 |
| `internal/mdview/static/js/web-url.js` (+`test/web-url.test.mjs`) | 신규 | URL 정규화 순수 함수 |
| `internal/mdview/static/js/webopen.js` | 신규 | 「웹 열기」 UI Worker |
| `internal/mdview/static/js/recorder.js` | 수정 | web start/stop/live 연동, `noteWebOpen`, `showToast` export, prompt 토스트 |
| `internal/mdview/static/js/app.js` | 수정 | `initWebOpen()` 배선 |
| `cmd/main.go` | 수정 | 종료 시 Chrome 정리·파이프라인 대기, transcribe 서브커맨드 prompt 생성 |
| `README.md`, spec | 수정 | 문서 |

---

### Task 0: 의존성과 툴체인

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: go.mod의 go 버전을 올리고 chromedp를 추가한다**

```bash
cd cli/mdview
sed -i '' 's/^go 1.24.3$/go 1.26/' go.mod
go get github.com/chromedp/chromedp@v0.16.0 github.com/chromedp/cdproto@v0.0.0-20260804232424-e85f50dbfd32
go mod tidy
```

Expected: `go: downloading go1.26…` 이 처음 한 번 나올 수 있다(GOTOOLCHAIN=auto). 끝나면 `go.mod`의 require에 chromedp·cdproto가 들어 있다.

- [ ] **Step 2: 기존 테스트가 새 툴체인에서도 통과하는지 확인한다**

Run: `go test ./... 2>&1 | tail -5`
Expected: `ok  	github.com/ryujt/mdview/internal/mdview` (logging 패키지는 `no test files` 가능)

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "build(mdview): Go 1.26 툴체인과 chromedp 의존성 추가

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 1: 타입 확장 (types.go, transcribe.go)

**Files:**
- Modify: `internal/mdview/types.go`
- Modify: `internal/mdview/transcribe.go:62-94` (RecordingEvent/RecordingMeta), `:430-476` (actionEntry/매핑/guide), `:488-497` (buildTranscriptJSON의 actionEntry 생성)
- Test: `internal/mdview/transcribe_test.go`

- [ ] **Step 1: 실패하는 테스트를 추가한다** — `transcribe_test.go` 끝에 붙인다.

```go
// web-* 이벤트는 action 이름이 문서 이벤트와 같은 어휘(open/click/select/copy)로 바뀌고
// url·selector 등 웹 필드가 보존되어야 한다.
func TestBuildTranscriptJSONWebEvents(t *testing.T) {
	meta := &RecordingMeta{
		Version: 1, Mode: "folder", Root: "/proj", DurationMs: 30000, DroppedWebEvents: 2,
		Events: []RecordingEvent{
			{T: 1000, Type: WebEventOpen, URL: "https://example.com/dash"},
			{T: 2000, Type: WebEventClick, URL: "https://example.com/dash", Title: "Dash",
				Selector: "main > span.v", Tag: "span", ID: "mau", Classes: "v hi",
				Text: "12,345", Heading: "핵심 지표", Context: "월간 활성 사용자 12,345"},
			{T: 3000, Type: WebEventDblClick, URL: "https://example.com/dash", Tag: "p", Selection: "활성"},
			{T: 4000, Type: WebEventCopy, URL: "https://example.com/dash", Tag: "p", Selection: "12,345"},
		},
	}
	data, err := buildTranscriptJSON(meta, &WhisperResult{}, DefaultTranscribeModel)
	if err != nil {
		t.Fatalf("buildTranscriptJSON error: %v", err)
	}
	var doc struct {
		Guide struct {
			Fields string `json:"fields"`
		} `json:"guide"`
		Timeline []map[string]any `json:"timeline"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("파싱 실패: %v\n%s", err, data)
	}
	wantActions := []string{"open", "click", "select", "copy"}
	if len(doc.Timeline) != len(wantActions) {
		t.Fatalf("timeline 길이 = %d, want %d\n%s", len(doc.Timeline), len(wantActions), data)
	}
	for i, want := range wantActions {
		if doc.Timeline[i]["action"] != want {
			t.Errorf("timeline[%d].action = %v, want %s", i, doc.Timeline[i]["action"], want)
		}
		if doc.Timeline[i]["url"] != "https://example.com/dash" {
			t.Errorf("timeline[%d].url = %v", i, doc.Timeline[i]["url"])
		}
		if _, has := doc.Timeline[i]["file"]; has {
			t.Errorf("timeline[%d]에 file 필드가 있음", i)
		}
	}
	click := doc.Timeline[1]
	for key, want := range map[string]string{
		"title": "Dash", "selector": "main > span.v", "tag": "span", "id": "mau",
		"classes": "v hi", "text": "12,345", "heading": "핵심 지표", "context": "월간 활성 사용자 12,345",
	} {
		if click[key] != want {
			t.Errorf("click.%s = %v, want %q", key, click[key], want)
		}
	}
	if !strings.Contains(doc.Guide.Fields, "url") {
		t.Errorf("guide.fields에 웹 이벤트 설명이 없음: %s", doc.Guide.Fields)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

Run: `go test ./internal/mdview/ -run TestBuildTranscriptJSONWebEvents 2>&1 | head`
Expected: 컴파일 에러 `undefined: WebEventOpen` 등.

- [ ] **Step 3: types.go에 상수와 필드를 추가한다** — `TranscribeJob` 바로 위에 상수 블록을, 구조체에 두 필드를 넣는다.

```go
// 웹 페이지 조작 이벤트 타입 (events.json의 type 값).
// 문서 이벤트(open/focus/word/copy)와 구분하기 위해 web- 접두를 둔다.
const (
	WebEventOpen     = "web-open"
	WebEventClick    = "web-click"
	WebEventDblClick = "web-dblclick"
	WebEventCopy     = "web-copy"
)
```

`TranscribeJob`의 `TranscriptRel` 다음에:

```go
	// Prompt는 항상 생성되는 prompt.txt 절대 경로 (후처리 성공 시)
	Prompt string `json:"prompt,omitempty"`
	// PromptRel은 folder 모드에서 루트 기준 상대 경로
	PromptRel string `json:"promptRel,omitempty"`
```

`RecordingResult.Transcribing` 주석을 `// Transcribing은 백그라운드 후처리(전사·프롬프트)가 시작되었는지 여부`로 바꾼다.

- [ ] **Step 4: transcribe.go의 RecordingEvent / RecordingMeta를 확장한다**

`RecordingEvent`의 `PDF` 필드 뒤에:

```go
	// 웹 페이지 조작(web-*) 전용 필드. 문서 이벤트에서는 비어 있다.
	URL      string `json:"url,omitempty"`
	Title    string `json:"title,omitempty"`
	Selector string `json:"selector,omitempty"`
	Tag      string `json:"tag,omitempty"`
	ID       string `json:"id,omitempty"`
	Classes  string `json:"classes,omitempty"`
	Heading  string `json:"heading,omitempty"`
	Context  string `json:"context,omitempty"`
```

`RecordingMeta`의 `Audio` 뒤에:

```go
	// DroppedWebEvents는 검증·상한으로 폐기된 웹 이벤트 수이다.
	DroppedWebEvents int `json:"droppedWebEvents,omitempty"`
```

- [ ] **Step 5: actionEntry·매핑·guide를 확장한다**

`actionEntry`의 `PDF` 뒤에:

```go
	URL      string `json:"url,omitempty"`
	Title    string `json:"title,omitempty"`
	Selector string `json:"selector,omitempty"`
	Tag      string `json:"tag,omitempty"`
	ID       string `json:"id,omitempty"`
	Classes  string `json:"classes,omitempty"`
	Heading  string `json:"heading,omitempty"`
	Context  string `json:"context,omitempty"`
```

`actionNameByEventType`를 다음으로 교체:

```go
var actionNameByEventType = map[string]string{
	"open":           "open",
	"focus":          "click",
	"word":           "select",
	"copy":           "copy",
	WebEventOpen:     "open",
	WebEventClick:    "click",
	WebEventDblClick: "select",
	WebEventCopy:     "copy",
}
```

`transcriptGuideText()`의 `Fields` 문자열 끝에 이어 붙인다:

```go
			"url이 있으면 웹 페이지 조작이다: file 대신 조작 당시 페이지 주소(url)·제목(title)과 요소 정보(selector/tag/id/classes), " +
			"요소 텍스트(text), 가장 가까운 제목(heading), 주변 텍스트(context)를 가진다. " +
			"selector는 다시 클릭할 정확한 경로가 아니라 요소를 알아볼 단서다.",
```

`buildTranscriptJSON`의 `actionEntry{...}` 생성에 웹 필드를 추가:

```go
		items = append(items, timed{at: e.T, rank: 1, entry: actionEntry{
			Kind: "action", Action: action, AtMs: e.T,
			File: e.File, Line: e.Line, LineEnd: e.LineEnd, Approx: e.Approx,
			Text: e.Text, Selection: e.Selection, PDF: e.PDF,
			URL: e.URL, Title: e.Title, Selector: e.Selector, Tag: e.Tag, ID: e.ID,
			Classes: e.Classes, Heading: e.Heading, Context: e.Context,
		}})
```

- [ ] **Step 6: 테스트 통과를 확인한다**

Run: `go test ./internal/mdview/ -run 'TestBuildTranscriptJSON' -v 2>&1 | tail -5`
Expected: `--- PASS: TestBuildTranscriptJSONWebEvents`, 기존 `TestBuildTranscriptJSON`도 PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/mdview/types.go internal/mdview/transcribe.go internal/mdview/transcribe_test.go
git commit -m "feat(mdview): 녹화 이벤트·전사 문서에 웹 페이지 조작 필드 추가

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 2: WebSession (웹 이벤트 검증·버퍼)

**Files:**
- Create: `internal/mdview/websession.go`
- Test: `internal/mdview/websession_test.go`

- [ ] **Step 1: 테스트를 작성한다** — `internal/mdview/websession_test.go`

```go
package mdview

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

const testStartEpochMs = 1755320000000

func webPayload(fields map[string]any) string {
	data, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func webClickPayload(atEpochMs int64, selector, url string) string {
	return webPayload(map[string]any{
		"type": "click", "atEpochMs": atEpochMs, "selector": selector, "url": url,
	})
}

func startedWebSession(t *testing.T) *WebSession {
	t.Helper()
	s := NewWebSession()
	s.Start(testStartEpochMs)
	return s
}

func TestWebSessionAddComputesElapsedTimeAndPrefixesType(t *testing.T) {
	s := startedWebSession(t)
	s.Add(webClickPayload(testStartEpochMs+12345, "div.card", "https://example.com"))

	events, dropped, start := s.Take()
	if len(events) != 1 {
		t.Fatalf("이벤트 %d개, want 1개", len(events))
	}
	if events[0].T != 12345 {
		t.Errorf("t = %d, want 12345", events[0].T)
	}
	if events[0].Type != WebEventClick || events[0].Selector != "div.card" {
		t.Errorf("이벤트 내용 불일치: %+v", events[0])
	}
	if dropped != 0 || start != testStartEpochMs {
		t.Errorf("dropped = %d, startEpochMs = %d", dropped, start)
	}
}

func TestWebSessionIgnoresEventsWhenNotRecording(t *testing.T) {
	s := NewWebSession()
	s.Add(webClickPayload(testStartEpochMs, "div", "https://example.com"))
	if _, count := s.Live(); count != 0 {
		t.Errorf("녹음 중이 아닌데 이벤트가 %d개 쌓임", count)
	}
	s.Start(testStartEpochMs)
	s.Add(webClickPayload(testStartEpochMs, "div", "https://example.com"))
	s.Freeze(1000)
	s.Add(webClickPayload(testStartEpochMs+500, "div", "https://example.com"))
	if _, count := s.Live(); count != 1 {
		t.Errorf("Freeze 이후에도 수집됨: %d개", count)
	}
}

func TestWebSessionTakeStopsCollecting(t *testing.T) {
	s := startedWebSession(t)
	s.Add(webClickPayload(testStartEpochMs+100, "div", "https://example.com"))
	if events, _, _ := s.Take(); len(events) != 1 {
		t.Fatalf("이벤트 %d개, want 1개", len(events))
	}
	s.Add(webClickPayload(testStartEpochMs+200, "div", "https://example.com"))
	if recording, count := s.Live(); recording || count != 0 {
		t.Errorf("Take 후 recording=%v events=%d, want false/0", recording, count)
	}
}

func TestWebSessionStartDiscardsPreviousBuffer(t *testing.T) {
	s := startedWebSession(t)
	s.Add(webClickPayload(testStartEpochMs+100, "div", "https://example.com"))
	s.Add(webPayload(map[string]any{"type": "bogus"}))
	s.Start(testStartEpochMs + 90000)
	events, dropped, start := s.Take()
	if len(events) != 0 || dropped != 0 || start != testStartEpochMs+90000 {
		t.Errorf("Start가 상태를 초기화하지 않음: events=%d dropped=%d start=%d", len(events), dropped, start)
	}
}

func TestWebSessionRejectsInvalidPayloads(t *testing.T) {
	s := startedWebSession(t)
	s.Add("not json")
	s.Add(`{"type":`)
	s.Add(webPayload(map[string]any{"type": "keydown", "atEpochMs": testStartEpochMs}))
	s.Add(webPayload(map[string]any{"type": WebEventClick, "atEpochMs": testStartEpochMs})) // 접두가 붙은 타입은 원문으로 허용하지 않는다
	s.Add(webPayload(map[string]any{"type": "click", "atEpochMs": testStartEpochMs, "text": strings.Repeat("가", maxWebPayloadBytes)}))
	events, dropped, _ := s.Take()
	if len(events) != 0 || dropped != 5 {
		t.Errorf("events=%d dropped=%d, want 0/5", len(events), dropped)
	}
}

func TestWebSessionTruncatesFields(t *testing.T) {
	s := startedWebSession(t)
	s.Add(webPayload(map[string]any{
		"type": "click", "atEpochMs": testStartEpochMs,
		"selector":  strings.Repeat("a", maxWebSelectorLen+50),
		"text":      strings.Repeat("가", maxWebTextLen+50),
		"context":   strings.Repeat("나", maxWebContextLen+50),
		"heading":   strings.Repeat("다", maxWebHeadingLen+50),
		"classes":   strings.Repeat("c", maxWebClassesLen+50),
		"id":        strings.Repeat("i", maxWebIDLen+50),
		"selection": strings.Repeat("라", maxWebSelectionLen+50),
		"title":     strings.Repeat("t", maxWebTitleLen+50),
		"url":       "https://example.com/" + strings.Repeat("u", maxWebURLLen),
	}))
	events, _, _ := s.Take()
	if len(events) != 1 {
		t.Fatalf("이벤트 %d개, want 1개", len(events))
	}
	e := events[0]
	limits := map[string]struct {
		got string
		max int
	}{
		"selector": {e.Selector, maxWebSelectorLen}, "text": {e.Text, maxWebTextLen},
		"context": {e.Context, maxWebContextLen}, "heading": {e.Heading, maxWebHeadingLen},
		"classes": {e.Classes, maxWebClassesLen}, "id": {e.ID, maxWebIDLen},
		"selection": {e.Selection, maxWebSelectionLen}, "title": {e.Title, maxWebTitleLen},
		"url": {e.URL, maxWebURLLen},
	}
	for name, l := range limits {
		if n := len([]rune(l.got)); n != l.max {
			t.Errorf("%s 길이 = %d, want %d", name, n, l.max)
		}
	}
}

func TestWebSessionClampsForgedTimestamps(t *testing.T) {
	s := startedWebSession(t)
	s.Add(webClickPayload(testStartEpochMs-999999, "div.past", "https://example.com"))
	s.Add(webClickPayload(time.Now().UnixMilli()+3600_000, "div.future", "https://example.com"))
	events, _, _ := s.Take()
	if len(events) != 2 {
		t.Fatalf("이벤트 %d개, want 2개", len(events))
	}
	if events[0].T != 0 {
		t.Errorf("과거로 위조된 t = %d, want 0", events[0].T)
	}
	if events[1].T <= 0 || events[1].T > time.Now().UnixMilli()-testStartEpochMs {
		t.Errorf("미래로 위조된 t = %d", events[1].T)
	}
}

func TestWebSessionStopsCollectingAtLimit(t *testing.T) {
	s := startedWebSession(t)
	for i := 0; i < maxWebSessionEvents+10; i++ {
		s.Add(webClickPayload(testStartEpochMs+int64(i), fmt.Sprintf("div:nth-of-type(%d)", i), "https://example.com"))
	}
	events, dropped, _ := s.Take()
	if len(events) != maxWebSessionEvents || dropped != 10 {
		t.Errorf("events=%d dropped=%d, want %d/10", len(events), dropped, maxWebSessionEvents)
	}
}

func TestWebSessionMergesDblClick(t *testing.T) {
	s := startedWebSession(t)
	const sel, url = "p.para", "https://example.com/doc"
	s.Add(webClickPayload(testStartEpochMs+1000, sel, url))
	s.Add(webClickPayload(testStartEpochMs+1100, "button.other", "https://other.example.com")) // 다른 탭
	s.Add(webClickPayload(testStartEpochMs+1200, sel, url))
	s.Add(webPayload(map[string]any{
		"type": "dblclick", "atEpochMs": testStartEpochMs + 1210, "selector": sel, "url": url, "selection": "오케스트레이터",
	}))
	events, _, _ := s.Take()
	if len(events) != 2 {
		t.Fatalf("이벤트 %d개, want 2개: %+v", len(events), events)
	}
	if events[0].Selector != "button.other" {
		t.Errorf("다른 탭의 click이 함께 제거됨: %+v", events)
	}
	if events[1].Type != WebEventDblClick || events[1].Selection != "오케스트레이터" {
		t.Errorf("dblclick이 남지 않음: %+v", events[1])
	}
}

func TestWebSessionFreezeClampsToDuration(t *testing.T) {
	s := startedWebSession(t)
	s.Add(webClickPayload(testStartEpochMs+500, "a", "https://example.com"))
	s.Add(webClickPayload(testStartEpochMs+90000, "b", "https://example.com"))
	if got := s.Freeze(60000); got != 2 {
		t.Errorf("Freeze() = %d, want 2", got)
	}
	events, _, _ := s.Take()
	if events[1].T != 60000 {
		t.Errorf("durationMs를 넘은 t = %d, want 60000", events[1].T)
	}
}

func TestWebSessionReset(t *testing.T) {
	s := startedWebSession(t)
	s.Add(webClickPayload(testStartEpochMs+100, "div", "https://example.com"))
	s.Reset()
	s.Add(webClickPayload(testStartEpochMs+200, "div", "https://example.com"))
	if recording, count := s.Live(); recording || count != 0 {
		t.Errorf("Reset 후 recording=%v events=%d", recording, count)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("  가나다라마  ", 3); got != "가나다" {
		t.Errorf("truncateRunes = %q", got)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

Run: `go test ./internal/mdview/ -run TestWebSession 2>&1 | head -3`
Expected: `undefined: NewWebSession`

- [ ] **Step 3: websession.go를 작성한다**

```go
package mdview

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/ryujt/mdview/internal/mdview/logging"
)

// 웹 이벤트 수집 정책 상수.
// 주입 스크립트는 임의 사이트와 같은 브라우저에서 돌기 때문에 payload는 신뢰할 수 없는 입력이다.
// inject/capture.js 상단의 같은 이름 상수는 payload를 줄이기 위한 1차 절단일 뿐이고 정본은 여기다.
const (
	maxWebPayloadBytes  = 8 << 10
	maxWebSessionEvents = 5000
	dblclickMergeMs     = 700
	dblclickScanBack    = 6
	dblclickMaxMerge    = 2

	maxWebURLLen       = 500
	maxWebTitleLen     = 200
	maxWebSelectorLen  = 300
	maxWebTagLen       = 40
	maxWebIDLen        = 100
	maxWebClassesLen   = 100
	maxWebTextLen      = 200
	maxWebHeadingLen   = 120
	maxWebContextLen   = 300
	maxWebSelectionLen = 200
)

// webEventTypeByRaw는 주입 스크립트가 보내는 타입(화이트리스트)을 events.json 타입으로 바꾼다.
var webEventTypeByRaw = map[string]string{
	"click":    WebEventClick,
	"dblclick": WebEventDblClick,
	"copy":     WebEventCopy,
}

// rawWebEvent는 주입 스크립트가 binding으로 보낸 원본 payload이다.
type rawWebEvent struct {
	Type      string `json:"type"`
	AtEpochMs int64  `json:"atEpochMs"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Selector  string `json:"selector"`
	Tag       string `json:"tag"`
	ID        string `json:"id"`
	Classes   string `json:"classes"`
	Text      string `json:"text"`
	Heading   string `json:"heading"`
	Context   string `json:"context"`
	Selection string `json:"selection"`
}

// WebSession은 녹음 중 웹 이벤트 버퍼를 소유하는 유일한 Service이다.
// 브라우저는 시각(Date.now)만 보내고 경과 시간 계산·절단·클램프는 모두 여기서 한다.
type WebSession struct {
	mu           sync.Mutex
	recording    bool
	startEpochMs int64
	events       []RecordingEvent
	dropped      int
	limitWarned  bool
}

// NewWebSession은 빈 세션을 만든다.
func NewWebSession() *WebSession {
	return &WebSession{}
}

// Start는 수집을 시작한다. 기존 버퍼는 항상 폐기한다.
func (s *WebSession) Start(startEpochMs int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recording = true
	s.startEpochMs = startEpochMs
	s.events = nil
	s.dropped = 0
	s.limitWarned = false
}

// Add는 원문 payload를 검증해 버퍼에 넣는다. 검증에 걸리면 폐기하고 경고만 남긴다.
func (s *WebSession) Add(payload string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.recording {
		return
	}
	if len(payload) > maxWebPayloadBytes {
		s.dropLocked("payload가 상한(%d바이트)을 초과: %d바이트", maxWebPayloadBytes, len(payload))
		return
	}
	var raw rawWebEvent
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		s.dropLocked("payload 파싱 실패: %v", err)
		return
	}
	eventType, ok := webEventTypeByRaw[raw.Type]
	if !ok {
		s.dropLocked("허용되지 않은 이벤트 타입: %q", raw.Type)
		return
	}
	if len(s.events) >= maxWebSessionEvents {
		if !s.limitWarned {
			s.limitWarned = true
			logging.Warn("[WebSession] 이벤트 상한 %d건 도달 — 이후 수집을 중단합니다", maxWebSessionEvents)
		}
		s.dropped++
		return
	}
	event := RecordingEvent{
		T:         s.clampEpochLocked(raw.AtEpochMs) - s.startEpochMs,
		Type:      eventType,
		URL:       truncateRunes(raw.URL, maxWebURLLen),
		Title:     truncateRunes(raw.Title, maxWebTitleLen),
		Selector:  truncateRunes(raw.Selector, maxWebSelectorLen),
		Tag:       truncateRunes(raw.Tag, maxWebTagLen),
		ID:        truncateRunes(raw.ID, maxWebIDLen),
		Classes:   truncateRunes(raw.Classes, maxWebClassesLen),
		Text:      truncateRunes(raw.Text, maxWebTextLen),
		Heading:   truncateRunes(raw.Heading, maxWebHeadingLen),
		Context:   truncateRunes(raw.Context, maxWebContextLen),
		Selection: truncateRunes(raw.Selection, maxWebSelectionLen),
	}
	if event.Type == WebEventDblClick {
		s.mergeDblClickLocked(event)
	}
	s.events = append(s.events, event)
}

// Live는 폴링용 현재 상태를 반환한다.
func (s *WebSession) Live() (recording bool, events int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recording, len(s.events)
}

// Freeze는 정지 시점에 수집을 멈추고 모든 t를 [0, durationMs]로 클램프한다. 버퍼는 유지한다.
func (s *WebSession) Freeze(durationMs int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recording = false
	if durationMs < 0 {
		durationMs = 0
	}
	for i := range s.events {
		switch {
		case s.events[i].T < 0:
			s.events[i].T = 0
		case s.events[i].T > durationMs:
			s.events[i].T = durationMs
		}
	}
	return len(s.events)
}

// Take는 이벤트·폐기 건수·시작 시각을 회수하고 버퍼를 비운다. 회수와 동시에 수집도 멈춘다.
func (s *WebSession) Take() (events []RecordingEvent, dropped int, startEpochMs int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	events, dropped, startEpochMs = s.events, s.dropped, s.startEpochMs
	s.recording = false
	s.events = nil
	s.dropped = 0
	s.limitWarned = false
	return events, dropped, startEpochMs
}

// Reset은 버퍼를 폐기하고 상태를 초기화한다.
func (s *WebSession) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recording = false
	s.startEpochMs = 0
	s.events = nil
	s.dropped = 0
	s.limitWarned = false
}

func (s *WebSession) clampEpochLocked(atEpochMs int64) int64 {
	now := time.Now().UnixMilli()
	if atEpochMs < s.startEpochMs {
		return s.startEpochMs
	}
	if atEpochMs > now {
		return now
	}
	return atEpochMs
}

// mergeDblClickLocked는 더블클릭이 만든 선행 click을 제거한다.
// 이벤트는 타깃별 채널로 오므로 다른 탭의 click이 끼어들 수 있어 버퍼 끝에서 몇 개를 훑는다.
func (s *WebSession) mergeDblClickLocked(dbl RecordingEvent) {
	from := len(s.events) - dblclickScanBack
	if from < 0 {
		from = 0
	}
	kept := s.events[:from]
	removed := 0
	for _, e := range s.events[from:] {
		if removed < dblclickMaxMerge && isDblClickSource(e, dbl) {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	s.events = kept
}

func isDblClickSource(e, dbl RecordingEvent) bool {
	if e.Type != WebEventClick || e.Selector != dbl.Selector || e.URL != dbl.URL {
		return false
	}
	gap := dbl.T - e.T
	return gap >= 0 && gap <= dblclickMergeMs
}

func (s *WebSession) dropLocked(format string, args ...any) {
	s.dropped++
	logging.Warn("[WebSession] 이벤트 폐기 — "+format, args...)
}

// truncateRunes는 문자열을 룬 단위로 자른다 (한글이 깨지지 않도록).
func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
```

- [ ] **Step 4: 통과를 확인한다**

Run: `go test ./internal/mdview/ -run 'TestWebSession|TestTruncateRunes' 2>&1 | tail -3`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/mdview/websession.go internal/mdview/websession_test.go
git commit -m "feat(mdview): 웹 페이지 조작 이벤트 검증·버퍼 WebSession 추가

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 3: prompt.go (prompt.txt 생성)

**Files:**
- Create: `internal/mdview/prompt.go`
- Test: `internal/mdview/prompt_test.go`

- [ ] **Step 1: 테스트를 작성한다** — `internal/mdview/prompt_test.go`

```go
package mdview

import (
	"strings"
	"testing"
)

func promptFixtureMeta() *RecordingMeta {
	return &RecordingMeta{
		Version: 1, Mode: "folder", Root: "/proj",
		StartedAt: "2026-10-18T14:03:11+09:00", DurationMs: 750000, Audio: "audio.webm",
		Events: []RecordingEvent{
			{T: 500, Type: "open", File: "docs/guide.md"},
			{T: 6000, Type: "focus", File: "docs/guide.md", Line: 42, LineEnd: 45, Text: "본문 한 줄"},
			{T: 7300, Type: "word", File: "docs/guide.md", Line: 43, LineEnd: 43, Text: "오케스트레이터"},
			{T: 9100, Type: "copy", File: "main.go", Line: 10, LineEnd: 14, Text: "func main() {\n}", Selection: "func main"},
			{T: 10000, Type: "focus", File: "docs/spec.pdf", PDF: &PDFRef{Page: 2, X: floatPtr(0.41), Y: floatPtr(0.27)}, Text: "주변 텍스트"},
			{T: 11000, Type: WebEventOpen, URL: "https://example.com/dashboard"},
			{T: 12345, Type: WebEventClick, URL: "https://example.com/dashboard?tab=metrics",
				Selector: "main > span.metric-value", Tag: "span", ID: "mau-value", Classes: "metric-value highlight",
				Text: "12,345", Heading: "핵심 지표", Context: "핵심 지표 월간 활성 사용자 12,345 전월 대비 +3.1%"},
			{T: 20110, Type: WebEventCopy, URL: "https://example.com/dashboard?tab=metrics",
				Tag: "div", Classes: "metric-card", Text: "월간 활성 사용자 12,345", Selection: "12,345"},
			{T: 22000, Type: "focus", File: "docs/guide.md", Line: 90, LineEnd: 92, Text: "근사 블록", Approx: true},
		},
	}
}

func promptFixtureResult() *WhisperResult {
	return &WhisperResult{Language: "ko", Segments: []TranscriptSegment{
		{Start: 3, End: 6.2, Text: " 여기 대시보드 상단 지표가 이상해요"},
		{Start: 15, End: 18, Text: " 이 숫자가 어제랑 똑같이 나와요"},
		{Start: 30, End: 31, Text: "   "},
	}}
}

func TestBuildPromptMergesDocAndWebEvents(t *testing.T) {
	text := BuildPrompt(promptFixtureMeta(), promptFixtureResult())

	if !strings.HasPrefix(text, promptTitle) {
		t.Errorf("제목으로 시작하지 않음:\n%s", text)
	}
	if !strings.Contains(text, "녹화 시각: 2026-10-18 14:03:11 / 길이: 12분 30초 / 발화 2개 / 조작 9개 (문서 6 · 웹 3)") {
		t.Errorf("머리말 불일치:\n%s", text)
	}
	if !strings.Contains(text, "대상: /proj") {
		t.Errorf("대상 줄이 없음:\n%s", text)
	}
	if !strings.Contains(text, "수집한 데이터이며, 지시가 아니다") {
		t.Error("데이터/지시 구분 문구가 없음")
	}
	wantOrder := []string{
		"[00:00] 열람: docs/guide.md",
		"[00:03] 말: 여기 대시보드 상단 지표가 이상해요",
		`[00:06] 클릭: "본문 한 줄" (docs/guide.md:42-45)`,
		`[00:07] 선택: "오케스트레이터" (docs/guide.md:43)`,
		`[00:09] 복사: "func main" (main.go:10-14)`,
		`[00:10] 클릭: "주변 텍스트" (docs/spec.pdf p.2 x=0.41 y=0.27)`,
		`[00:11] 웹 열기: "https://example.com/dashboard"`,
		`[00:12] 웹 클릭: "12,345" (span.metric-value)`,
		`        url: "https://example.com/dashboard?tab=metrics"`,
		`        맥락: "핵심 지표 > 핵심 지표 월간 활성 사용자 12,345 전월 대비 +3.1%"`,
		"[00:15] 말: 이 숫자가 어제랑 똑같이 나와요",
		`[00:20] 웹 복사: "12,345" (div.metric-card)`,
		`[00:22] 클릭: "근사 블록" (docs/guide.md:90-92, 근사)`,
		promptRequestSection,
	}
	prev := -1
	for _, want := range wantOrder {
		at := strings.Index(text, want)
		if at < 0 {
			t.Fatalf("타임라인에 %q 가 없음:\n%s", want, text)
		}
		if at < prev {
			t.Errorf("순서가 어긋남: %q", want)
		}
		prev = at
	}
}

func TestBuildPromptWithoutTranscript(t *testing.T) {
	text := BuildPrompt(promptFixtureMeta(), nil)
	if !strings.Contains(text, "/ "+promptNoSpeech+" /") {
		t.Errorf("전사 생략 표기가 없음:\n%s", text)
	}
	if strings.Contains(text, "] 말: ") {
		t.Error("전사 없는 프롬프트에 발화 줄이 있음")
	}
	if !strings.Contains(text, `[00:12] 웹 클릭: "12,345" (span.metric-value)`) {
		t.Error("조작 타임라인이 비어 있음")
	}
}

func TestBuildPromptFileModeTargetAndEmpty(t *testing.T) {
	text := BuildPrompt(&RecordingMeta{Mode: "file", File: "/proj/README.md", DurationMs: 4500}, &WhisperResult{})
	if !strings.Contains(text, "대상: /proj/README.md") {
		t.Errorf("file 모드 대상이 없음:\n%s", text)
	}
	if !strings.Contains(text, "길이: 4초 / 발화 0개 / 조작 0개 (문서 0 · 웹 0)") {
		t.Errorf("빈 녹화 머리말 불일치:\n%s", text)
	}
}

func TestPromptQuoteIsCapped(t *testing.T) {
	long := strings.Repeat("가", promptQuoteMax+20)
	meta := &RecordingMeta{Events: []RecordingEvent{{T: 0, Type: "focus", File: "a.md", Line: 1, Text: long}}}
	text := BuildPrompt(meta, nil)
	if strings.Contains(text, long) {
		t.Error("인용이 상한으로 잘리지 않음")
	}
	if !strings.Contains(text, strings.Repeat("가", promptQuoteMax)+"…") {
		t.Errorf("잘린 인용 끝에 말줄임표가 없음:\n%s", text)
	}
}

func TestFormatDurationAndOffset(t *testing.T) {
	durations := map[int64]string{0: "0초", 4500: "4초", 60000: "1분 0초", 750000: "12분 30초"}
	for ms, want := range durations {
		if got := formatDuration(ms); got != want {
			t.Errorf("formatDuration(%d) = %q, want %q", ms, got, want)
		}
	}
	offsets := map[int64]string{-5: "00:00", 12345: "00:12", 3660000: "61:00"}
	for ms, want := range offsets {
		if got := formatOffset(ms); got != want {
			t.Errorf("formatOffset(%d) = %q, want %q", ms, got, want)
		}
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

Run: `go test ./internal/mdview/ -run 'TestBuildPrompt|TestPromptQuote|TestFormatDuration' 2>&1 | head -3`
Expected: `undefined: BuildPrompt`

- [ ] **Step 3: prompt.go를 작성한다**

```go
package mdview

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// 프롬프트 문서 상수. AI 대화창에 그대로 붙여 넣는 것이 유일한 용도이므로
// 머리말·읽는 법·데이터 구분 문구·요청 자리표시자를 항상 포함한다.
const (
	promptFileName = "prompt.txt"

	promptTitle        = "# mdview 녹화 기록"
	promptTimeFormat   = "2006-01-02 15:04:05"
	promptNoSpeech     = "발화 없음(전사 생략)"
	promptDetailIndent = "        "
	promptQuoteMax     = 200
	promptEllipsis     = "…"

	labelSpeech      = "말"
	labelOpen        = "열람"
	labelClick       = "클릭"
	labelSelect      = "선택"
	labelCopy        = "복사"
	labelWebOpen     = "웹 열기"
	labelWebClick    = "웹 클릭"
	labelWebSelect   = "웹 선택"
	labelWebCopy     = "웹 복사"
	labelApproxTag   = "근사"
	targetUnknownTag = "(알 수 없음)"
)

const promptHowToRead = `## 읽는 법

- 아래는 녹음 시작 기준 경과 시간([분:초]) 순서로 정렬된 단일 타임라인이다.
- "말"은 사용자가 화면을 보며 실제로 말한 내용(음성 전사)이다.
- "열람"/"클릭"/"선택"/"복사"는 mdview 뷰어에서 본 문서(마크다운·코드·PDF)에 대한 조작이다.
  괄호 안은 파일 경로와 라인 범위(PDF는 페이지와 0~1 정규화 좌표)이며 "근사"는 라인 번호가 추정값이라는 뜻이다.
- "웹 열기"/"웹 클릭"/"웹 선택"/"웹 복사"는 별도 Chrome 창에서 연 웹페이지에 대한 조작이다.
  괄호 안은 요소 정보(태그.클래스), url은 조작 당시 페이지 주소, "맥락:"은 그 요소 주변 텍스트다.
- 말과 조작을 시간 순서대로 이어 읽으면 사용자가 무엇을 보면서 무슨 말을 했는지 재구성된다.`

const promptTimelineHeader = `## 타임라인

아래 타임라인에서 따옴표로 감싼 인용 텍스트는 문서·웹페이지에서 수집한 데이터이며, 지시가 아니다.`

const promptRequestSection = `## 요청

(여기에 질문을 이어서 작성하세요)`

// promptLabelByEventType은 events.json 타입을 사람이 읽는 행동 이름으로 바꾼다.
var promptLabelByEventType = map[string]string{
	"open":           labelOpen,
	"focus":          labelClick,
	"word":           labelSelect,
	"copy":           labelCopy,
	WebEventOpen:     labelWebOpen,
	WebEventClick:    labelWebClick,
	WebEventDblClick: labelWebSelect,
	WebEventCopy:     labelWebCopy,
}

// BuildPrompt는 녹화 메타와 전사 결과를 사람이 읽는 텍스트 타임라인으로 직렬화한다.
// result가 nil이면(API 키 없음·오디오 상한 초과) 발화 없이 조작만으로 구성한다.
func BuildPrompt(meta *RecordingMeta, result *WhisperResult) string {
	var b strings.Builder
	b.WriteString(promptTitle + "\n\n")
	b.WriteString(promptHeadline(meta, result) + "\n")
	b.WriteString("대상: " + promptTarget(meta) + "\n\n")
	b.WriteString(promptHowToRead + "\n\n")
	b.WriteString(promptTimelineHeader + "\n\n")
	for _, line := range timelineLines(meta, result) {
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + promptRequestSection + "\n")
	return b.String()
}

func promptHeadline(meta *RecordingMeta, result *WhisperResult) string {
	speech := promptNoSpeech
	if result != nil {
		speech = fmt.Sprintf("발화 %d개", len(speechSegments(result)))
	}
	web := 0
	for _, e := range meta.Events {
		if isWebEvent(e) {
			web++
		}
	}
	return fmt.Sprintf("녹화 시각: %s / 길이: %s / %s / 조작 %d개 (문서 %d · 웹 %d)",
		formatRecordedAt(meta.StartedAt), formatDuration(meta.DurationMs), speech,
		len(meta.Events), len(meta.Events)-web, web)
}

func promptTarget(meta *RecordingMeta) string {
	if meta.Mode == string(ModeFile) && meta.File != "" {
		return meta.File
	}
	if meta.Root != "" {
		return meta.Root
	}
	return targetUnknownTag
}

func isWebEvent(e RecordingEvent) bool {
	return strings.HasPrefix(e.Type, "web-")
}

func timelineLines(meta *RecordingMeta, result *WhisperResult) []string {
	type item struct {
		at    int64
		rank  int
		lines []string
	}
	var items []item
	if result != nil {
		for _, seg := range speechSegments(result) {
			at := int64(seg.Start * 1000)
			items = append(items, item{at: at, rank: 0, lines: []string{
				fmt.Sprintf("[%s] %s: %s", formatOffset(at), labelSpeech, strings.TrimSpace(seg.Text)),
			}})
		}
	}
	for _, e := range meta.Events {
		items = append(items, item{at: e.T, rank: 1, lines: eventLines(e)})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].at != items[j].at {
			return items[i].at < items[j].at
		}
		return items[i].rank < items[j].rank
	})
	var lines []string
	for _, it := range items {
		lines = append(lines, it.lines...)
	}
	return lines
}

// eventLines는 조작 하나를 헤더 줄 + 들여쓴 상세 줄들로 만든다.
func eventLines(e RecordingEvent) []string {
	if isWebEvent(e) {
		return webEventLines(e)
	}
	return docEventLines(e)
}

func docEventLines(e RecordingEvent) []string {
	label := promptLabelByEventType[e.Type]
	if label == "" {
		label = e.Type
	}
	head := fmt.Sprintf("[%s] %s:", formatOffset(e.T), label)
	if e.Type == "open" {
		return []string{head + " " + e.File}
	}
	if q := docQuote(e); q != "" {
		head += fmt.Sprintf(" %q", capQuote(q))
	}
	loc := docLocation(e)
	if e.Approx {
		loc += ", " + labelApproxTag
	}
	if loc != "" {
		head += fmt.Sprintf(" (%s)", loc)
	}
	return []string{head}
}

func webEventLines(e RecordingEvent) []string {
	label := promptLabelByEventType[e.Type]
	if label == "" {
		label = e.Type
	}
	head := fmt.Sprintf("[%s] %s:", formatOffset(e.T), label)
	if e.Type == WebEventOpen {
		return []string{head + fmt.Sprintf(" %q", e.URL)}
	}
	if q := webQuote(e); q != "" {
		head += fmt.Sprintf(" %q", capQuote(q))
	}
	if el := elementLabel(e); el != "" {
		head += fmt.Sprintf(" (%s)", el)
	}
	lines := []string{head}
	if e.URL != "" {
		lines = append(lines, fmt.Sprintf("%surl: %q", promptDetailIndent, e.URL))
	}
	if ctx := eventContext(e); ctx != "" {
		lines = append(lines, fmt.Sprintf("%s맥락: %q", promptDetailIndent, capQuote(ctx)))
	}
	return lines
}

// docQuote는 문서 조작을 대표하는 인용 문자열이다. 복사는 실제 복사된 문자열, 그 외는 위치의 원문.
func docQuote(e RecordingEvent) string {
	if e.Type == "copy" && e.Selection != "" {
		return e.Selection
	}
	return e.Text
}

// webQuote는 선택·복사면 선택 문자열, 클릭이면 요소 텍스트다.
func webQuote(e RecordingEvent) string {
	if e.Type != WebEventClick && e.Selection != "" {
		return e.Selection
	}
	return e.Text
}

// docLocation은 "파일:시작-끝" 또는 PDF "파일 p.N x=.. y=.." 표기를 만든다.
func docLocation(e RecordingEvent) string {
	if e.PDF != nil {
		loc := fmt.Sprintf("%s p.%d", e.File, e.PDF.Page)
		if e.PDF.X != nil && e.PDF.Y != nil {
			loc += fmt.Sprintf(" x=%.2f y=%.2f", *e.PDF.X, *e.PDF.Y)
		}
		return loc
	}
	switch {
	case e.Line > 0 && e.LineEnd > e.Line:
		return fmt.Sprintf("%s:%d-%d", e.File, e.Line, e.LineEnd)
	case e.Line > 0:
		return fmt.Sprintf("%s:%d", e.File, e.Line)
	default:
		return e.File
	}
}

func elementLabel(e RecordingEvent) string {
	if e.Tag == "" {
		return ""
	}
	if first := firstClass(e.Classes); first != "" {
		return e.Tag + "." + first
	}
	if e.ID != "" {
		return e.Tag + "#" + e.ID
	}
	return e.Tag
}

func firstClass(classes string) string {
	for _, c := range strings.Fields(classes) {
		return c
	}
	return ""
}

func eventContext(e RecordingEvent) string {
	switch {
	case e.Heading != "" && e.Context != "":
		return e.Heading + " > " + e.Context
	case e.Heading != "":
		return e.Heading
	default:
		return e.Context
	}
}

// capQuote는 긴 원문(복사된 코드 블록 등)이 프롬프트를 덮지 않도록 인용을 상한에서 자른다.
func capQuote(s string) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= promptQuoteMax {
		return s
	}
	return string(runes[:promptQuoteMax]) + promptEllipsis
}

func speechSegments(result *WhisperResult) []TranscriptSegment {
	var out []TranscriptSegment
	for _, seg := range result.Segments {
		if strings.TrimSpace(seg.Text) != "" {
			out = append(out, seg)
		}
	}
	return out
}

func formatOffset(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	total := ms / 1000
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

func formatDuration(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	total := ms / 1000
	if total < 60 {
		return fmt.Sprintf("%d초", total)
	}
	return fmt.Sprintf("%d분 %d초", total/60, total%60)
}

func formatRecordedAt(startedAt string) string {
	if startedAt == "" {
		return targetUnknownTag
	}
	t, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return startedAt
	}
	return t.Format(promptTimeFormat)
}
```

- [ ] **Step 4: 통과를 확인한다**

Run: `go test ./internal/mdview/ -run 'TestBuildPrompt|TestPromptQuote|TestFormatDuration' -v 2>&1 | grep -E '^(---|ok|FAIL)'`
Expected: 모두 `--- PASS`. 주의: `%q`는 한글을 그대로 두고 `"`만 이스케이프한다. 테스트 문자열이 안 맞으면 출력을 보고 **구현이 아니라 기대 문자열의 오타**를 먼저 의심한다.

- [ ] **Step 5: Commit**

```bash
git add internal/mdview/prompt.go internal/mdview/prompt_test.go
git commit -m "feat(mdview): 문서·웹 조작과 발화를 병합한 prompt.txt 생성기 추가

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 4: Pipeline (전사 가능 시 전사 → prompt.txt 항상)

**Files:**
- Create: `internal/mdview/pipeline.go`
- Modify: `internal/mdview/transcribe.go` (`TranscribeSkipReason` 추가, `TranscribeRecording` 직후 위치)
- Test: `internal/mdview/pipeline_test.go`

- [ ] **Step 1: 테스트를 작성한다** — `internal/mdview/pipeline_test.go`

```go
package mdview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), recordingFilePerm); err != nil {
		t.Fatalf("fixture write error (%s): %v", name, err)
	}
}

func readFixture(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("%s 읽기 실패: %v", name, err)
	}
	return string(data)
}

// pipelineFixture는 오디오와 events.json이 들어 있는 녹화 디렉토리를 만든다.
func pipelineFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir, "audio.webm", "FAKEAUDIO")
	meta := RecordingMeta{
		Version: 1, Mode: "folder", Root: "/proj", StartedAt: "2026-10-18T14:03:11+09:00",
		DurationMs: 30000, Audio: "audio.webm",
		Events: []RecordingEvent{
			{T: 1000, Type: "focus", File: "doc.md", Line: 3, LineEnd: 3, Text: "본문"},
			{T: 12345, Type: WebEventClick, URL: "https://example.com", Tag: "span", Classes: "metric-value", Text: "12,345"},
		},
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, eventsFileName, string(encoded))
	return dir
}

// 키가 없어도 조작 기록만으로 prompt.txt가 만들어져야 한다.
func TestPipelineWithoutTranscription(t *testing.T) {
	t.Setenv(openaiKeyEnv, "")
	t.Setenv("HOME", t.TempDir())
	dir := pipelineFixture(t)

	job := NewPipeline().Run(dir)
	if job.State != TranscribeStateDone {
		t.Fatalf("job.State = %q (%s)", job.State, job.Error)
	}
	if job.Transcript != "" {
		t.Errorf("전사를 생략했는데 transcript 경로가 있음: %q", job.Transcript)
	}
	if _, err := os.Stat(filepath.Join(dir, transcriptFileName)); !os.IsNotExist(err) {
		t.Error("전사를 생략했는데 transcript.json이 생성됨")
	}
	if job.Prompt != filepath.Join(dir, promptFileName) {
		t.Errorf("job.Prompt = %q", job.Prompt)
	}
	text := readFixture(t, dir, promptFileName)
	if !strings.Contains(text, promptNoSpeech) || !strings.Contains(text, `[00:12] 웹 클릭: "12,345" (span.metric-value)`) {
		t.Errorf("프롬프트 내용 불일치:\n%s", text)
	}
}

// 전사 결과(audio.json)가 이미 있으면 API 없이 재사용해 transcript.json과 prompt.txt를 모두 만든다.
func TestPipelineWithExistingTranscription(t *testing.T) {
	t.Setenv(openaiKeyEnv, "sk-test")
	dir := pipelineFixture(t)
	writeFixture(t, dir, "audio.json",
		`{"text":"지표가 이상해요","language":"ko","segments":[{"start":3.0,"end":6.0,"text":" 지표가 이상해요"}]}`)

	job := NewPipeline().Run(dir)
	if job.State != TranscribeStateDone {
		t.Fatalf("job.State = %q (%s)", job.State, job.Error)
	}
	if job.Transcript != filepath.Join(dir, transcriptFileName) {
		t.Errorf("job.Transcript = %q", job.Transcript)
	}
	if _, err := os.Stat(job.Transcript); err != nil {
		t.Errorf("transcript.json이 없음: %v", err)
	}
	text := readFixture(t, dir, promptFileName)
	if !strings.Contains(text, "발화 1개") || !strings.Contains(text, "[00:03] 말: 지표가 이상해요") {
		t.Errorf("발화가 반영되지 않음:\n%s", text)
	}
}

func TestPipelineFailsWithoutMeta(t *testing.T) {
	t.Setenv(openaiKeyEnv, "")
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	writeFixture(t, dir, "audio.webm", "FAKEAUDIO")
	if job := NewPipeline().Run(dir); job.State != TranscribeStateError {
		t.Errorf("%s 이 없는데 성공으로 처리됨: %q", eventsFileName, job.State)
	}
}

func TestTranscribeSkipReason(t *testing.T) {
	t.Setenv(openaiKeyEnv, "")
	t.Setenv("HOME", t.TempDir())
	dir := pipelineFixture(t)
	if reason := TranscribeSkipReason(dir, DefaultTranscribeModel); !strings.Contains(reason, "API 키") {
		t.Errorf("키 없음 사유가 아님: %q", reason)
	}
	if reason := TranscribeSkipReason(dir, localWhisperDefault); reason != "" {
		t.Errorf("로컬 모델은 키와 무관해야 함: %q", reason)
	}
	t.Setenv(openaiKeyEnv, "sk-test")
	if reason := TranscribeSkipReason(dir, DefaultTranscribeModel); reason != "" {
		t.Errorf("키가 있으면 생략하지 않아야 함: %q", reason)
	}
	if reason := TranscribeSkipReason(t.TempDir(), DefaultTranscribeModel); !strings.Contains(reason, "오디오") {
		t.Errorf("오디오 없음 사유가 아님: %q", reason)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

Run: `go test ./internal/mdview/ -run 'TestPipeline|TestTranscribeSkipReason' 2>&1 | head -3`
Expected: `undefined: NewPipeline`

- [ ] **Step 3: transcribe.go에 TranscribeSkipReason을 추가한다** — `TranscribeRecording` 함수 바로 뒤.

```go
// TranscribeSkipReason은 자동 전사를 생략해야 하는 이유를 반환한다 (빈 문자열이면 전사 가능).
// 이미 whisper 결과가 있으면 키·크기와 무관하게 재사용할 수 있고, 로컬 CLI 모델은 API 제약을 받지 않는다.
func TranscribeSkipReason(dir, model string) string {
	audioPath, err := findRecordingAudio(dir)
	if err != nil {
		return "오디오 파일이 없습니다"
	}
	if _, err := os.Stat(whisperResultPath(dir, audioPath)); err == nil {
		return ""
	}
	if !isAPIModel(model) {
		return ""
	}
	if _, err := ResolveOpenAIKey(); err != nil {
		return "OpenAI API 키가 없습니다"
	}
	if size := fileSize(audioPath); size > maxAPIAudioBytes {
		return fmt.Sprintf("오디오가 %dMB로 API 업로드 상한(%dMB)을 초과합니다", size>>20, maxAPIAudioBytes>>20)
	}
	return ""
}
```

- [ ] **Step 4: pipeline.go를 작성한다**

```go
package mdview

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ryujt/mdview/internal/mdview/logging"
)

// Pipeline은 녹음 종료 후 "저장된 녹화 → 전사(가능 시) → prompt.txt" 단계를 진행하는 Sub-Orchestrator이다.
type Pipeline struct{}

// NewPipeline은 파이프라인을 만든다.
func NewPipeline() *Pipeline {
	return &Pipeline{}
}

// Run은 녹화 디렉토리 하나를 후처리하고 결과 상태를 반환한다.
// 전사는 조건이 맞을 때만 수행하며, 전사가 없어도 프롬프트 생성은 진행한다.
func (p *Pipeline) Run(dir string) *TranscribeJob {
	return p.buildPrompt(dir, transcribeIfPossible(dir))
}

// RunAfterTranscribe는 이미 전사를 마친(또는 생략한) 녹화에 대해 프롬프트 생성부터 진행한다.
func (p *Pipeline) RunAfterTranscribe(dir string) *TranscribeJob {
	return p.buildPrompt(dir, loadExistingWhisperResult(dir))
}

func (p *Pipeline) buildPrompt(dir string, result *WhisperResult) *TranscribeJob {
	job := &TranscribeJob{ID: filepath.Base(dir), State: TranscribeStateDone, Dir: dir}
	meta, err := loadRecordingMeta(dir)
	if err != nil {
		logging.Error("[Pipeline] %s 읽기 실패: %v", eventsFileName, err)
		logging.LogProcessEvent("execution", "step_err", eventsFileName+" 읽기 실패", nil)
		job.State = TranscribeStateError
		job.Error = err.Error()
		return job
	}
	if result != nil {
		job.Transcript = filepath.Join(dir, transcriptFileName)
	}
	promptPath := filepath.Join(dir, promptFileName)
	text := BuildPrompt(meta, result)
	if err := os.WriteFile(promptPath, []byte(text), recordingFilePerm); err != nil {
		logging.Error("[Pipeline] %s 저장 실패: %v", promptFileName, err)
		logging.LogProcessEvent("synthesis", "error", promptFileName+" 저장 실패", nil)
		job.State = TranscribeStateError
		job.Error = err.Error()
		return job
	}
	job.Prompt = promptPath
	logging.Info("[Pipeline] 프롬프트 생성: %s (%d바이트)", promptPath, len(text))
	logging.LogProcessEvent("synthesis", "end", "프롬프트 생성: "+promptFileName, nil)
	return job
}

// transcribeIfPossible은 전사 조건을 확인한 뒤 전사를 시도한다. 생략·실패 모두 nil을 반환할 뿐 중단하지 않는다.
func transcribeIfPossible(dir string) *WhisperResult {
	if reason := TranscribeSkipReason(dir, DefaultTranscribeModel); reason != "" {
		logging.Info("[Pipeline] 전사 생략: %s", reason)
		fmt.Printf("전사를 생략합니다 (%s) — 조작 기록만으로 프롬프트를 만듭니다.\n", reason)
		return nil
	}
	if _, err := TranscribeRecording(dir, DefaultTranscribeModel, ""); err != nil {
		logging.Error("[Pipeline] 전사 실패: %v", err)
		fmt.Fprintf(os.Stderr, "전사 실패 (%v) — 조작 기록만으로 프롬프트를 만듭니다\n", err)
		return nil
	}
	return loadExistingWhisperResult(dir)
}

// loadExistingWhisperResult는 저장된 전사 결과를 읽는다 (없으면 nil).
func loadExistingWhisperResult(dir string) *WhisperResult {
	audioPath, err := findRecordingAudio(dir)
	if err != nil {
		return nil
	}
	result, err := loadWhisperResult(whisperResultPath(dir, audioPath))
	if err != nil {
		return nil
	}
	return result
}
```

- [ ] **Step 5: 통과를 확인한다**

Run: `go test ./internal/mdview/ -run 'TestPipeline|TestTranscribeSkipReason' -v 2>&1 | grep -E '^(---|ok|FAIL)'`
Expected: 4개 모두 PASS. `TestPipelineWithExistingTranscription`은 `audio.json`이 있어 `TranscribeRecording`이 API를 호출하지 않고 재사용한다.

- [ ] **Step 6: Commit**

```bash
git add internal/mdview/pipeline.go internal/mdview/pipeline_test.go internal/mdview/transcribe.go
git commit -m "feat(mdview): 녹화 후처리 파이프라인(전사 가능 시 전사 → prompt.txt) 추가

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 5: 서버 — 웹 세션 API, 업로드 병합, 파이프라인 시작

이 Task에서는 Chrome을 건드리지 않는다. `/api/web/open`은 Task 7에서 붙인다.

**Files:**
- Modify: `internal/mdview/recording.go` (`mergeWebEvents` 추가)
- Modify: `internal/mdview/server.go`
- Modify: `internal/mdview/recording_test.go` (기존 업로드 테스트 격리)
- Test: `internal/mdview/server_web_test.go`

- [ ] **Step 1: 테스트를 작성한다** — `internal/mdview/server_web_test.go`

```go
package mdview

import (
	"encoding/json"
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

const testLocalOrigin = "http://localhost:8642"

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

func (ts *webTestServer) post(t *testing.T, path, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if headers == nil {
		headers = map[string]string{originHeader: testLocalOrigin}
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

	res := ts.post(t, apiWebStartPath, `{"startEpochMs":`+itoa(testStartEpochMs)+`}`, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d, want 200", res.StatusCode)
	}
	ts.server.web.Add(webClickPayload(testStartEpochMs+1000, "div.card", "https://example.com"))
	ts.server.web.Add(webClickPayload(testStartEpochMs+90000, "div.late", "https://example.com"))

	live := decodeJSON[map[string]any](t, ts.get(t, apiWebLivePath))
	if live["recording"] != true || live["events"] != float64(2) || live["chrome"] != false {
		t.Errorf("live = %v, want recording:true events:2 chrome:false", live)
	}

	stop := decodeJSON[map[string]any](t, ts.post(t, apiWebStopPath, `{"durationMs":60000}`, nil))
	if stop["ok"] != true || stop["events"] != float64(2) {
		t.Errorf("stop = %v", stop)
	}
	ts.server.web.Add(webClickPayload(testStartEpochMs+61000, "div.after", "https://example.com"))
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
			{originHeader: testLocalOrigin, secFetchSiteHeader: "cross-site"},
		} {
			res := ts.post(t, path, "{}", h)
			res.Body.Close()
			if res.StatusCode != http.StatusForbidden {
				t.Errorf("%s (헤더 %v) status = %d, want 403", path, h, res.StatusCode)
			}
		}
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		res, _ := http.DefaultClient.Do(req)
		res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("GET %s status = %d, want 405", path, res.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+apiWebLivePath, nil)
	req.Header.Set(originHeader, "https://evil.example.com")
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("크로스오리진 live status = %d, want 403", res.StatusCode)
	}
}

// 업로드 시 서버가 수집한 웹 이벤트가 문서 이벤트와 t순으로 병합되고, 후처리가 prompt.txt를 만든다.
func TestHandleRecordingMergesWebEventsAndRunsPipeline(t *testing.T) {
	ts := newWebTestServer(t)
	ts.post(t, apiWebStartPath, `{"startEpochMs":`+itoa(testStartEpochMs)+`}`, nil).Body.Close()
	ts.server.web.Add(webClickPayload(testStartEpochMs+1500, "div.card", "https://example.com"))
	ts.server.web.Add(webPayload(map[string]any{"type": "bogus"})) // dropped 1
	ts.post(t, apiWebStopPath, `{"durationMs":5000}`, nil).Body.Close()

	meta := `{"version":1,"mode":"folder","root":"/proj","startedAt":"2026-10-18T14:03:11+09:00","durationMs":5000,` +
		`"events":[{"t":1000,"type":"open","file":"a.md"},{"t":1500,"type":"focus","file":"a.md","line":2,"lineEnd":2,"text":"x"},{"t":3000,"type":"word","file":"a.md","line":4,"lineEnd":4,"text":"y"}]}`
	body, contentType := buildRecordingForm(t, "AUDIOBYTES", "audio/webm;codecs=opus", meta)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+apiRecordingPath, body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set(originHeader, testLocalOrigin)
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
```

- [ ] **Step 2: 실패를 확인한다**

Run: `go test ./internal/mdview/ -run 'TestWebSessionLifecycleAPI|TestWebAPIRejects|TestHandleRecordingMerges|TestMergeWebEvents' 2>&1 | head -3`
Expected: `undefined: apiWebStartPath`

- [ ] **Step 3: recording.go에 mergeWebEvents를 추가한다** — `const ( jsonKeyAudio … )` 블록을 확장하고 함수를 `summarizeRecordingEvents` 앞에 둔다. import에 `"sort"` 추가.

```go
const (
	jsonKeyAudio            = "audio"
	jsonKeySavedAt          = "savedAt"
	jsonKeyEvents           = "events"
	jsonKeyDroppedWebEvents = "droppedWebEvents"
)

// mergeWebEvents는 서버가 수집한 웹 이벤트를 브라우저가 보낸 meta.events에 합치고 t순으로 안정 정렬한다.
// 문서 이벤트는 브라우저가 시간순으로 보냈고 먼저 들어가므로, 같은 t에서는 문서 이벤트가 앞선다.
func mergeWebEvents(meta map[string]any, webEvents []RecordingEvent, dropped int) {
	existing, _ := meta[jsonKeyEvents].([]any)
	merged := make([]any, 0, len(existing)+len(webEvents))
	merged = append(merged, existing...)
	for _, e := range webEvents {
		if m := eventToMap(e); m != nil {
			merged = append(merged, m)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool { return eventTime(merged[i]) < eventTime(merged[j]) })
	meta[jsonKeyEvents] = merged
	if dropped > 0 {
		meta[jsonKeyDroppedWebEvents] = dropped
	} else {
		delete(meta, jsonKeyDroppedWebEvents)
	}
}

// eventToMap은 타입 있는 이벤트를 JSON 왕복으로 map으로 바꾼다 (omitempty 규칙을 그대로 적용하기 위해).
func eventToMap(e RecordingEvent) map[string]any {
	data, err := json.Marshal(e)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

// eventTime은 map 이벤트의 t를 읽는다 (JSON 디코딩 결과는 float64).
func eventTime(v any) float64 {
	m, ok := v.(map[string]any)
	if !ok {
		return 0
	}
	t, _ := m["t"].(float64)
	return t
}
```

- [ ] **Step 4: server.go를 수정한다**

(a) 상수 블록에 추가:

```go
	apiWebStartPath  = "/api/web/start"
	apiWebStopPath   = "/api/web/stop"
	apiWebCancelPath = "/api/web/cancel"
	apiWebLivePath   = "/api/web/live"

	secFetchSiteHeader = "Sec-Fetch-Site"
	secFetchSiteSame   = "same-origin"

	methodNotAllowedMessage = "method not allowed"
	forbiddenOriginMessage  = "forbidden origin"
```

(b) `isLocalOrigin` 아래에 엄격 검증을 추가:

```go
// hasLocalOrigin은 웹 세션 상태를 바꾸는 요청에 쓰는 더 엄격한 검증이다.
// mdview는 적대적일 수 있는 임의 사이트를 같은 머신의 Chrome에 띄우므로
// Origin 헤더가 존재하고 로컬일 것을 요구하며, Sec-Fetch-Site가 있으면 same-origin이어야 한다.
func hasLocalOrigin(r *http.Request) bool {
	if r.Header.Get(originHeader) == "" {
		return false
	}
	if site := r.Header.Get(secFetchSiteHeader); site != "" && site != secFetchSiteSame {
		return false
	}
	return isLocalOrigin(r)
}
```

(c) `Server` 구조체에 필드 추가 (`transcribeJobs` 뒤):

```go
	// web은 녹음 중 웹 페이지 조작 이벤트 버퍼이다 (WebCapture → WebSession.Add).
	web *WebSession
	// pipeline은 녹화 저장 후 후처리(전사·프롬프트)이다.
	pipeline *Pipeline
	// pending은 진행 중인 후처리 goroutine이다. 종료 시 여기서 기다린다.
	pending sync.WaitGroup
```

(d) 두 생성자의 `&Server{…}`를 공통 헬퍼로 바꾼다:

```go
func newServer(mode Mode, hub *Hub) *Server {
	return &Server{
		mode: mode, hub: hub,
		transcribeJobs: map[string]*TranscribeJob{},
		web:            NewWebSession(),
		pipeline:       NewPipeline(),
	}
}
```

`NewFileServer`: `s := newServer(ModeFile, hub); s.filePath = abs; s.workDir = workDir; return s, nil`
`NewFolderServer`: `s := newServer(ModeFolder, hub); s.root = abs; return s, nil`

(e) `Handler()`에 라우트 추가:

```go
	mux.HandleFunc(apiWebStartPath, s.handleWebStart)
	mux.HandleFunc(apiWebStopPath, s.handleWebStop)
	mux.HandleFunc(apiWebCancelPath, s.handleWebCancel)
	mux.HandleFunc(apiWebLivePath, s.handleWebLive)
```

(f) 핸들러들을 `handleRecording` 앞에 추가:

```go
// handleWebStart는 녹음 시작 시각(epoch ms)을 받아 웹 이벤트 수집을 시작한다. 기존 버퍼는 폐기된다.
func (s *Server) handleWebStart(w http.ResponseWriter, r *http.Request) {
	if !s.acceptWebStateChange(w, r) {
		return
	}
	var body struct {
		StartEpochMs int64 `json:"startEpochMs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if body.StartEpochMs <= 0 {
		body.StartEpochMs = time.Now().UnixMilli()
	}
	s.web.Start(body.StartEpochMs)
	logging.Info("[Web] 수집 시작 (startEpochMs=%d)", body.StartEpochMs)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleWebStop은 정지 시점에 수집을 멈추고 t를 녹화 길이 안으로 클램프한다.
func (s *Server) handleWebStop(w http.ResponseWriter, r *http.Request) {
	if !s.acceptWebStateChange(w, r) {
		return
	}
	var body struct {
		DurationMs int64 `json:"durationMs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	count := s.web.Freeze(body.DurationMs)
	logging.Info("[Web] 수집 정지 (durationMs=%d, 이벤트 %d개)", body.DurationMs, count)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "events": count})
}

// handleWebCancel은 페이지 이탈 시 버퍼를 폐기한다.
func (s *Server) handleWebCancel(w http.ResponseWriter, r *http.Request) {
	if !s.acceptWebStateChange(w, r) {
		return
	}
	s.web.Reset()
	w.WriteHeader(http.StatusNoContent)
}

// handleWebLive는 녹음 중 1초 폴링에 Chrome 상태와 수집 건수를 답한다.
func (s *Server) handleWebLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, methodNotAllowedMessage, http.StatusMethodNotAllowed)
		return
	}
	if !isLocalOrigin(r) {
		http.Error(w, forbiddenOriginMessage, http.StatusForbidden)
		return
	}
	recording, events := s.web.Live()
	writeJSON(w, http.StatusOK, map[string]any{"chrome": s.webAlive(), "recording": recording, "events": events})
}

// webAlive는 Chrome 창이 떠 있는지 반환한다 (Task 7에서 WebOpener로 교체).
func (s *Server) webAlive() bool {
	return false
}

// acceptWebStateChange는 웹 세션 상태 변경 요청의 메서드와 오리진을 검사한다.
func (s *Server) acceptWebStateChange(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, methodNotAllowedMessage, http.StatusMethodNotAllowed)
		return false
	}
	if !hasLocalOrigin(r) {
		logging.Warn("[Web] 상태 변경 요청 거부: origin=%q site=%q path=%s",
			r.Header.Get(originHeader), r.Header.Get(secFetchSiteHeader), r.URL.Path)
		http.Error(w, forbiddenOriginMessage, http.StatusForbidden)
		return false
	}
	return true
}
```

(g) `handleRecording`에서 meta 디코딩 직후, `SaveRecording` 호출 전에 병합을 넣는다:

```go
	// 웹 이벤트의 소유자는 서버다. 회수와 동시에 수집이 멈춘다 (/api/web/stop이 실패했어도).
	webEvents, dropped, _ := s.web.Take()
	mergeWebEvents(meta, webEvents, dropped)
```

그리고 `transcribing := s.startAutoTranscribe(dir)` 를 `s.startPipeline(dir)` 로, 응답의 `Transcribing: transcribing` 을 `Transcribing: true` 로 바꾼다. 주석 `// API 키가 있으면 백그라운드에서 자동 전사한다.`는 `// 후처리(전사 가능 시 전사 → prompt.txt)를 백그라운드로 시작한다.`로.

(h) `startAutoTranscribe` 전체를 다음으로 교체:

```go
// startPipeline은 후처리를 백그라운드로 시작한다 (업로드 응답은 즉시 돌려준다).
func (s *Server) startPipeline(dir string) {
	id := filepath.Base(dir)
	s.setJob(&TranscribeJob{ID: id, State: TranscribeStateRunning, Dir: dir})
	logging.Info("[Pipeline] 후처리 시작: %s", id)
	s.pending.Add(1)
	go func() {
		defer s.pending.Done()
		job := s.pipeline.Run(dir)
		job.TranscriptRel = s.relToRoot(job.Transcript)
		job.PromptRel = s.relToRoot(job.Prompt)
		s.setJob(job)
		if job.State == TranscribeStateError {
			logging.Error("[Pipeline] 후처리 실패 (%s): %s", id, job.Error)
			fmt.Fprintf(os.Stderr, "후처리 실패 (%s): %s\n", id, job.Error)
			return
		}
		logging.Info("[Pipeline] 후처리 완료: %s → %s", id, job.Prompt)
		fmt.Printf("완료 — 프롬프트 저장: %s\n", job.Prompt)
	}()
}

// relToRoot는 folder 모드에서 루트 아래 경로를 슬래시 상대 경로로 바꾼다 (뷰어에서 바로 열기용). 그 외엔 빈 문자열.
func (s *Server) relToRoot(abs string) string {
	if s.mode != ModeFolder || abs == "" {
		return ""
	}
	rel, err := filepath.Rel(s.root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}

// WaitPipelines는 진행 중인 후처리가 모두 끝날 때까지 기다린다 (종료 경로·테스트 정리용).
func (s *Server) WaitPipelines() {
	s.pending.Wait()
}

// PipelinesPending은 아직 끝나지 않은 후처리가 있는지 반환한다 (종료 안내 출력용).
func (s *Server) PipelinesPending() bool {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	for _, job := range s.transcribeJobs {
		if job.State == TranscribeStateRunning {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: 기존 업로드 테스트를 격리한다** — `recording_test.go`의 `TestHandleRecordingFolderMode`, `TestHandleRecordingFileMode`, `TestHandleRecordingRejections` 각각 첫 줄에 추가하고, `defer ts.Close()`를 아래처럼 바꾼다:

```go
	t.Setenv(openaiKeyEnv, "")
	t.Setenv("HOME", t.TempDir())
```

```go
	defer func() {
		server.WaitPipelines()
		ts.Close()
	}()
```

이유: 이제 업로드마다 후처리 goroutine이 TempDir에 prompt.txt를 쓰므로, 정리 전에 끝나야 하고 로컬 `~/.config/mdview/config.json`의 실제 키로 API를 부르면 안 된다.

- [ ] **Step 6: 전체 테스트 통과를 확인한다**

Run: `go test ./... 2>&1 | tail -3`
Expected: `ok`. 실패하면 `-run` 이름으로 좁혀 원인을 본다.

- [ ] **Step 7: Commit**

```bash
git add internal/mdview/recording.go internal/mdview/server.go internal/mdview/recording_test.go internal/mdview/server_web_test.go
git commit -m "feat(mdview): 웹 세션 API와 업로드 시 웹 이벤트 병합, 후처리 파이프라인 연결

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 6: Chrome 기동·주입·수집 (webview 코드 이식)

이 Task는 단위 테스트 없이 빌드·vet만 확인한다 (실제 Chrome은 Task 9의 수동 E2E에서 검증).

**Files:**
- Create: `internal/mdview/chrome.go`, `internal/mdview/webbrowser.go`, `internal/mdview/webcapture.go`, `internal/mdview/inject/capture.js`
- Modify: `internal/mdview/config.go` (`ConfigDir` 추가)

- [ ] **Step 1: config.go에 ConfigDir을 추가하고 ConfigPath가 그것을 쓰게 한다**

```go
// ConfigDir은 mdview 설정 디렉토리(~/.config/mdview)를 반환한다. Chrome 전용 프로필도 이 아래에 둔다.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("홈 디렉토리를 찾을 수 없습니다: %w", err)
	}
	return filepath.Join(home, ".config", configDirName), nil
}

// ConfigPath는 설정 파일 경로를 반환한다.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}
```

- [ ] **Step 2: chrome.go를 만든다** — webview `chrome.go` 복사 후 패키지명·환경변수만 변경.

```bash
cp ../webview/internal/webview/chrome.go internal/mdview/chrome.go
sed -i '' -e 's/^package webview$/package mdview/' -e 's/WEBVIEW_CHROME_PATH/MDVIEW_CHROME_PATH/' -e 's/^\/\/ webview는 /\/\/ mdview는 /' internal/mdview/chrome.go
grep -n "webview\|WEBVIEW" internal/mdview/chrome.go
```

Expected: grep 결과 없음.

- [ ] **Step 3: inject/capture.js를 만든다** — binding·설치 플래그 이름만 바꾼다.

```bash
mkdir -p internal/mdview/inject
cp ../webview/internal/webview/inject/capture.js internal/mdview/inject/capture.js
sed -i '' -e "s/__webviewCaptureEvent__/__mdviewCaptureEvent__/" -e "s/__webviewInstalled__/__mdviewInstalled__/" -e "s#internal/webview/session.go#internal/mdview/websession.go#" internal/mdview/inject/capture.js
grep -n "webview" internal/mdview/inject/capture.js
```

Expected: grep 결과 없음.

- [ ] **Step 4: webcapture.go를 작성한다** — webview `capture.go`에서 제외 타깃(컨트롤 페이지)을 없앤 판이다.

```go
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

	"github.com/ryujt/mdview/internal/mdview/logging"
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
			go func() { events <- ev }()
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
	delete(c.pages, id)
	empty := len(c.pages) == 0 && !c.closed
	if empty {
		c.closed = true
	}
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
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
```

- [ ] **Step 5: webbrowser.go를 작성한다** — 컨트롤 창·마이크 권한 없이, 첫 탭이 바로 대상 URL을 연다.

```go
package mdview

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/chromedp/cdproto/cdp"
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
		chromedp.Flag("window-size", browseWindowSize),
	)
	rootCtx, rootCancel := chromedp.NewContext(allocCtx)
	b := &WebBrowser{allocCancel: allocCancel, rootCtx: rootCtx, rootCancel: rootCancel}

	if err := chromedp.Run(rootCtx, chromedp.Navigate(startURL)); err != nil {
		b.Close()
		return nil, fmt.Errorf("Chrome 기동 실패 (같은 프로필로 mdview가 이미 실행 중인지 확인하세요): %w", err)
	}
	logging.Info("[WebBrowser] 준비 완료: %s", startURL)
	return b, nil
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
```

- [ ] **Step 6: 빌드와 vet, 기존 테스트를 확인한다**

Run: `go build ./... && go vet ./... && go test ./... 2>&1 | tail -3`
Expected: 에러 없음, `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/mdview/config.go internal/mdview/chrome.go internal/mdview/webbrowser.go internal/mdview/webcapture.go internal/mdview/inject/capture.js
git commit -m "feat(mdview): 전용 프로필 Chrome 기동과 격리 월드 조작 수집 Worker 이식

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 7: WebOpener와 `/api/web/open`, 종료 처리

**Files:**
- Create: `internal/mdview/webopen.go`
- Test: `internal/mdview/webopen_test.go`, `internal/mdview/server_web_test.go` (추가)
- Modify: `internal/mdview/server.go`, `cmd/main.go`

- [ ] **Step 1: 테스트를 작성한다** — `internal/mdview/webopen_test.go`

```go
package mdview

import (
	"errors"
	"testing"
)

func TestValidateWebURL(t *testing.T) {
	ok := map[string]string{
		"https://example.com/a?b=1": "https://example.com/a?b=1",
		"  http://localhost:3000 ":  "http://localhost:3000",
	}
	for in, want := range ok {
		got, err := ValidateWebURL(in)
		if err != nil || got != want {
			t.Errorf("ValidateWebURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "example.com", "file:///etc/passwd", "javascript:alert(1)", "ftp://x", "https://", "::bad"} {
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
```

`server_web_test.go`에 추가:

```go
func TestWebOpenAPIErrors(t *testing.T) {
	ts := newWebTestServer(t)
	ts.server.webOpener.launch = func(string) (*WebBrowser, error) { return nil, ErrChromeUnavailable }

	cases := []struct {
		body string
		want int
	}{
		{`{"url":"file:///etc/passwd"}`, http.StatusBadRequest},
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
```

`server_web_test.go`의 import에 `"errors"`를 추가한다.

- [ ] **Step 2: 실패를 확인한다**

Run: `go test ./internal/mdview/ -run 'TestValidateWebURL|TestWebOpener|TestWebOpenAPIErrors' 2>&1 | head -3`
Expected: `undefined: ValidateWebURL`

- [ ] **Step 3: webopen.go를 작성한다**

```go
package mdview

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/ryujt/mdview/internal/mdview/logging"
)

// ErrInvalidWebURL은 http/https가 아니거나 호스트가 없는 URL에 반환된다.
var ErrInvalidWebURL = errors.New("http/https URL만 열 수 있습니다")

// ValidateWebURL은 사용자가 입력한 URL을 검증하고 정규화한 문자열을 반환한다.
func ValidateWebURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", ErrInvalidWebURL
	}
	return u.String(), nil
}

// WebOpener는 전용 Chrome의 수명을 소유한다. 첫 열기에서 기동하고, 이후에는 같은 창에 탭을 연다.
// 모든 창이 닫히거나 Chrome이 죽으면 정리하고 다음 열기에서 다시 기동한다.
type WebOpener struct {
	mu      sync.Mutex
	browser *WebBrowser
	onEvent func(payload string)
	// launch는 테스트에서 교체하기 위한 기동 함수이다.
	launch func(url string) (*WebBrowser, error)
}

// NewWebOpener는 수집한 이벤트 payload를 onEvent로 보고하는 WebOpener를 만든다.
func NewWebOpener(onEvent func(string)) *WebOpener {
	return &WebOpener{onEvent: onEvent, launch: LaunchWebBrowser}
}

// Open은 URL을 Chrome에서 연다. 새로 기동했으면 launched=true.
func (o *WebOpener) Open(raw string) (launched bool, err error) {
	target, err := ValidateWebURL(raw)
	if err != nil {
		return false, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.browser != nil {
		if err := o.browser.OpenTab(target); err == nil {
			return false, nil
		} else {
			logging.Warn("[WebOpener] 탭 열기 실패 — Chrome을 다시 기동합니다: %v", err)
			o.browser.Close()
			o.browser = nil
		}
	}
	b, err := o.launch(target)
	if err != nil {
		return false, err
	}
	capture := NewWebCapture(b.RootCtx())
	capture.OnEvent = o.onEvent
	capture.OnAllTargetsClosed = func() { o.release(b) }
	if err := capture.Start(); err != nil {
		b.Close()
		return false, fmt.Errorf("이벤트 수집 시작 실패: %w", err)
	}
	o.browser = b
	go func() {
		<-b.Done()
		o.release(b)
	}()
	return true, nil
}

// release는 주어진 인스턴스가 아직 현재 브라우저일 때만 정리한다 (뒤늦은 콜백이 새 인스턴스를 닫지 않도록).
func (o *WebOpener) release(b *WebBrowser) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.browser != b {
		return
	}
	o.browser = nil
	b.Close()
	logging.Info("[WebOpener] Chrome 정리됨")
}

// Alive는 Chrome이 떠 있는지 반환한다.
func (o *WebOpener) Alive() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.browser != nil
}

// Close는 Chrome을 종료한다 (mdview 종료 경로).
func (o *WebOpener) Close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.browser != nil {
		o.browser.Close()
		o.browser = nil
	}
}
```

- [ ] **Step 4: server.go에 /api/web/open과 CloseWeb을 붙인다**

상수: `apiWebOpenPath = "/api/web/open"`. 구조체 필드 `web` 뒤에:

```go
	// webOpener는 전용 Chrome 수명을 소유한다 (수집 payload는 web.Add로 간다).
	webOpener *WebOpener
```

`newServer`에서 `s := &Server{…}` 뒤에 `s.webOpener = NewWebOpener(s.web.Add)` (구조체 리터럴을 변수에 받은 뒤 설정하고 `return s`).

라우트: `mux.HandleFunc(apiWebOpenPath, s.handleWebOpen)`.

`webAlive()`를 교체:

```go
func (s *Server) webAlive() bool {
	return s.webOpener.Alive()
}
```

핸들러와 종료 메서드 추가 (handleWebStart 앞):

```go
// handleWebOpen은 URL을 전용 Chrome 창에서 연다. Chrome 미설치·잘못된 URL은 400, 기동 실패는 500.
func (s *Server) handleWebOpen(w http.ResponseWriter, r *http.Request) {
	if !s.acceptWebStateChange(w, r) {
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	launched, err := s.webOpener.Open(body.URL)
	switch {
	case errors.Is(err, ErrInvalidWebURL), errors.Is(err, ErrChromeUnavailable):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		logging.Error("[Web] 열기 실패 (%s): %v", body.URL, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	logging.Info("[Web] 열기: %s (launched=%v)", body.URL, launched)
	logging.LogProcessEvent("execution", "step_ok", "웹 열기: "+body.URL, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "launched": launched})
}

// CloseWeb은 전용 Chrome을 종료한다 (mdview 종료 경로).
func (s *Server) CloseWeb() {
	s.webOpener.Close()
}
```

- [ ] **Step 5: cmd/main.go를 수정한다**

(a) `runFileMode`와 `runFolderMode`의 끝부분을 같은 형태로 바꾼다:

```go
	waitForSignal()
	logging.LogProcessEvent("end", "end", "종료 시그널 수신", nil)
	shutdown(server)
```

파일 끝에 추가:

```go
// shutdown은 전용 Chrome을 닫고 진행 중인 후처리(전사·프롬프트)를 기다린다.
// 녹음 중이던 오디오는 브라우저 메모리에만 있으므로 여기서 구할 수 없다.
func shutdown(server *mdview.Server) {
	server.CloseWeb()
	if server.PipelinesPending() {
		fmt.Println("후처리를 마무리하는 중입니다… (전사·프롬프트 생성)")
	}
	server.WaitPipelines()
}
```

(b) `runTranscribe`를 다음으로 교체 (전사 생략 시에도 prompt.txt를 만든다):

```go
// runTranscribe는 녹화 전사 서브커맨드를 실행한다. 전사를 생략해야 하는 경우에도 prompt.txt는 만든다.
func runTranscribe(args []string) {
	fs := flag.NewFlagSet("transcribe", flag.ExitOnError)
	model := fs.String("model", mdview.DefaultTranscribeModel,
		"전사 모델 (whisper-1: OpenAI API, 그 외(large-v3 등): 로컬 whisper CLI)")
	lang := fs.String("lang", "", "발화 언어 코드 (예: ko). 비우면 자동 감지")
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, transcribeUsage)
		os.Exit(1)
	}
	dir := fs.Arg(0)

	logging.StartSession()
	logging.LogProcessEvent("start", "start", "전사 시작: "+dir, nil)
	if reason := mdview.TranscribeSkipReason(dir, *model); reason != "" {
		fmt.Fprintf(os.Stderr, "전사를 생략합니다 (%s) — 조작 기록만으로 프롬프트를 만듭니다.\n", reason)
		logging.Warn("[Transcribe] 전사 생략: %s", reason)
	} else if _, err := mdview.TranscribeRecording(dir, *model, *lang); err != nil {
		exitTranscribe("전사 실패", err)
	}
	job := mdview.NewPipeline().RunAfterTranscribe(dir)
	if job.State == mdview.TranscribeStateError {
		exitTranscribe("프롬프트 생성 실패", errors.New(job.Error))
	}
	logging.LogProcessEvent("end", "end", "전사 완료: "+filepath.Base(dir), nil)
	logging.EndSession()
	if job.Transcript != "" {
		fmt.Printf("전사 문서 생성: %s\n", job.Transcript)
	}
	fmt.Printf("프롬프트 생성: %s\n", job.Prompt)
}

// exitTranscribe는 전사 서브커맨드를 에러 한 번만 출력하고 종료한다.
func exitTranscribe(label string, err error) {
	logging.Error("[Transcribe] %s: %v", label, err)
	logging.LogProcessEvent("end", "error", label, nil)
	logging.EndSession()
	fmt.Fprintf(os.Stderr, "%s: %v\n", label, err)
	os.Exit(1)
}
```

import에 `"errors"` 추가. `usage` 문자열의 transcribe 설명 마지막 줄 뒤에 `              전사 여부와 무관하게 AI에게 붙여 넣을 prompt.txt를 생성한다.` 를 추가한다.

- [ ] **Step 6: 통과를 확인한다**

Run: `go build ./... && go vet ./... && go test ./... 2>&1 | tail -3`
Expected: `ok`

- [ ] **Step 7: Commit**

```bash
git add internal/mdview/webopen.go internal/mdview/webopen_test.go internal/mdview/server.go internal/mdview/server_web_test.go cmd/main.go
git commit -m "feat(mdview): 웹 열기 API와 Chrome 수명 관리, 종료 시 Chrome 정리·후처리 대기

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 8: 프론트엔드 — 툴바 「웹 열기」, 녹음 연동, 상태 표시

**Files:**
- Create: `internal/mdview/static/js/web-url.js`, `internal/mdview/static/js/webopen.js`
- Test: `test/web-url.test.mjs` (cli/mdview 아래, node 내장 테스트 러너)
- Modify: `internal/mdview/static/index.html`, `internal/mdview/static/css/style.css`, `internal/mdview/static/js/recorder.js`, `internal/mdview/static/js/app.js`

- [ ] **Step 1: URL 정규화 테스트를 작성한다** — `test/web-url.test.mjs`

```js
import test from 'node:test';
import assert from 'node:assert/strict';
import { normalizeWebURL } from '../internal/mdview/static/js/web-url.js';

test('scheme이 없으면 https를 붙인다', () => {
  assert.equal(normalizeWebURL('example.com/a'), 'https://example.com/a');
  assert.equal(normalizeWebURL('  localhost:3000 '), 'http://localhost:3000'); // 로컬 호스트는 http 기본 (검토 반영)
});

test('http/https는 그대로 둔다', () => {
  assert.equal(normalizeWebURL('http://a.b/c?d=1'), 'http://a.b/c?d=1');
  assert.equal(normalizeWebURL('HTTPS://A.B'), 'HTTPS://A.B');
});

test('빈 입력과 다른 scheme은 빈 문자열', () => {
  assert.equal(normalizeWebURL(''), '');
  assert.equal(normalizeWebURL('   '), '');
  assert.equal(normalizeWebURL('file:///etc/passwd'), '');
  assert.equal(normalizeWebURL('javascript:alert(1)'), '');
});
```

- [ ] **Step 2: 실패를 확인한다**

Run: `node --test test/web-url.test.mjs 2>&1 | tail -3`
Expected: `Cannot find module … web-url.js`

- [ ] **Step 3: web-url.js를 작성한다** (검토 반영: `localhost`·`127.0.0.1`·`[::1]`·`*.localhost`는 `http://`를 붙이고, 앞의 `//`는 제거한다. 최종 코드는 저장소의 `web-url.js`를 정본으로 본다)

```js
// web-url.js — 툴바 입력값을 열 수 있는 URL로 정규화하는 순수 함수
//
// 정본 검증은 서버(ValidateWebURL)가 한다. 여기서는 사용자가 scheme을 생략했을 때
// https를 붙여 주고, http/https 이외의 scheme은 서버로 보내지 않는다.

const HTTP_SCHEME = /^https?:\/\//i;
const ANY_SCHEME = /^[a-z][a-z0-9+.-]*:(?!\d+(?:[/?#]|$))/i; // host:port 는 scheme 이 아니다

/**
 * @param {string} input 사용자 입력
 * @returns {string} 열 URL. 열 수 없는 입력이면 빈 문자열.
 */
export function normalizeWebURL(input) {
  const value = String(input || '').trim();
  if (!value) return '';
  if (HTTP_SCHEME.test(value)) return value;
  if (ANY_SCHEME.test(value)) return '';
  return `https://${value}`;
}
```

- [ ] **Step 4: 통과를 확인한다**

Run: `node --test test/web-url.test.mjs 2>&1 | grep -E '^# (pass|fail)'`
Expected: `# pass 3`, `# fail 0`

- [ ] **Step 5: index.html 툴바에 입력창·버튼을 추가한다** — `<span id="job-status" hidden></span>` 바로 앞에 넣는다.

```html
          <input id="web-url" type="text" inputmode="url" autocomplete="off" spellcheck="false"
                 placeholder="https://… 웹 페이지 열기" title="전용 Chrome 창에서 열 웹 페이지 주소" aria-label="열 웹 페이지 주소" />
          <button id="web-open" type="button" title="전용 Chrome 창에서 열기 (녹음 중 클릭·복사가 함께 기록됨)">웹 열기</button>
```

- [ ] **Step 6: style.css에 스타일을 추가한다** — `/* 백그라운드 작업(업로드/전사) 상태 칩 */` 주석 앞에 넣는다.

```css
/* 웹 페이지 열기 (전용 Chrome 창) */
#web-url {
  width: 220px;
  height: 28px;
  padding: 0 8px;
  font: inherit;
  font-size: 12px;
  color: #24292e;
  background: #ffffff;
  border: 1px solid #e1e4e8;
  border-radius: 6px;
}

#web-url:focus {
  outline: none;
  border-color: #0366d6;
  box-shadow: 0 0 0 2px rgba(3, 102, 214, 0.2);
}

#web-open {
  height: 28px;
  padding: 0 10px;
  font: inherit;
  font-size: 12px;
  color: #24292e;
  background: #ffffff;
  border: 1px solid #e1e4e8;
  border-radius: 6px;
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.15s ease, border-color 0.15s ease;
}

#web-open:hover {
  background: #f3f4f6;
  border-color: #0366d6;
}

#web-open:disabled {
  opacity: 0.55;
  cursor: default;
}

@media (max-width: 768px) {
  #web-url { width: 140px; }
}
```

- [ ] **Step 7: recorder.js를 수정한다**

(a) 상수 블록에 추가:

```js
const API_WEB_START_PATH = '/api/web/start';
const API_WEB_STOP_PATH = '/api/web/stop';
const API_WEB_LIVE_PATH = '/api/web/live';
const WEB_LIVE_POLL_MS = 1000;
```

(b) 상태 변수 블록(`let activeDeviceLabel = '';` 뒤)에 추가:

```js
let webLiveTimer = null;   // 녹음 중 웹 이벤트 수 폴링 타이머
let webChrome = false;     // 전용 Chrome 창이 떠 있는지 (마지막 폴링 기준)
let webEventCount = 0;     // 서버가 수집한 웹 이벤트 수 (마지막 폴링 기준)
```

(c) `noteFileOpen` 뒤에 추가:

```js
/** 툴바에서 웹 페이지를 열었을 때 호출된다 (webopen.js). 녹음 중이면 web-open 이벤트로 기록한다. */
export function noteWebOpen(url) {
  if (!recording || !url) return;
  pushEvent({ type: 'web-open', url });
}
```

(d) `startRecording`에서 `t0 = performance.now();` `recording = true;` 두 줄을 다음으로 교체 (시작 시각은 t0과 같은 순간의 epoch):

```js
  t0 = performance.now();
  const startEpochMs = Date.now();
  recording = true;
  notifyWebStart(startEpochMs);
  startWebLivePoll();
```

(e) `finalizeRecording`을 async로 바꾸고 업로드 전에 웹 수집을 멈춘다. 시그니처 `function finalizeRecording() {` → `async function finalizeRecording() {`. 본문 첫 두 줄 `const durationMs = elapsedMs(); recording = false;` 바로 뒤에:

```js
  stopWebLivePoll();
  await notifyWebStop(durationMs);
```

(f) 전사 상태 영역(`// 전사 진행 상태` 주석 블록) 앞에 웹 연동 함수들을 추가:

```js
// ---------------------------------------------------------------------------
// 웹 페이지 조작 수집 연동 (서버 WebSession)
// ---------------------------------------------------------------------------

/** 녹음 시작 시각(epoch ms)을 서버에 알린다. Chrome이 아직 없어도 보낸다 (세션 상태만 켜짐). */
async function notifyWebStart(startEpochMs) {
  try {
    await fetch(API_WEB_START_PATH, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ startEpochMs }),
    });
  } catch (err) {
    console.warn('웹 수집 시작 알림 실패:', err);
  }
}

/** 정지 시점을 서버에 알린다. 실패해도 업로드는 계속한다 (업로드가 수집을 멈춘다). */
async function notifyWebStop(durationMs) {
  try {
    await fetch(API_WEB_STOP_PATH, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ durationMs }),
    });
  } catch (err) {
    console.warn('웹 수집 정지 알림 실패:', err);
  }
}

/** 녹음 중 1초마다 Chrome 상태와 웹 이벤트 수를 읽어 칩을 갱신한다. */
function startWebLivePoll() {
  stopWebLivePoll();
  webChrome = false;
  webEventCount = 0;
  const tick = async () => {
    try {
      const res = await fetch(API_WEB_LIVE_PATH);
      if (res.ok) {
        const live = await res.json();
        webChrome = Boolean(live.chrome);
        webEventCount = Number(live.events) || 0;
        updateEventCount();
      }
    } catch {
      /* 다음 폴링에서 재시도 */
    }
    if (recording) webLiveTimer = setTimeout(tick, WEB_LIVE_POLL_MS);
  };
  tick();
}

function stopWebLivePoll() {
  if (webLiveTimer) {
    clearTimeout(webLiveTimer);
    webLiveTimer = null;
  }
}
```

(g) `updateEventCount`를 교체:

```js
function updateEventCount() {
  if (!recording) return;
  const web = webChrome ? ` · 웹 ${webEventCount}` : '';
  ui.eventsCount.textContent = `이벤트 ${events.length}${web}`;
}
```

(h) `handleBeforeUnload`는 **변경하지 않는다.** 검토에서 밝혀진 대로 서버의 웹 버퍼는 프로세스 전역이라, 녹음 중이 아닌 다른 탭이 이탈하며 cancel을 보내면 녹음 중인 탭의 버퍼를 지울 수 있다. 서버는 녹음 중 cancel을 무시하도록 바뀌었고, 프론트는 cancel을 호출하지 않는다(다음 녹음의 start가 버퍼를 비운다).

(i-0) `uploadRecording`의 `else` 분기(`transcribing`이 false일 때 수동 전사를 안내하던 블록)는 서버가 항상 후처리를 시작하므로 죽은 코드다. `if (result.transcribing) { … } else { … }`를 if 없이 성공 분기 본문만 남기도록 정리한다.

(i) `uploadRecording`의 성공 분기 토스트 문구를 바꾼다. `if (result.transcribing) {` 블록 안을:

```js
      setJobStatus('후처리 중', { spin: true });
      pollTranscribeStatus(result.id);
      showToast(
        `녹화 저장 완료 (이벤트 ${meta.events.length}개)\n${result.dir}\n` +
          '후처리(전사·prompt.txt 생성)가 백그라운드에서 진행 중입니다 (완료 시 알림)'
      );
```

(j) `pollTranscribeStatus`의 done 분기 `setJobStatus('전사 완료', { done: true });` → `setJobStatus('후처리 완료', { done: true });`, error 분기 문구 `자동 전사 실패:` → `후처리 실패:`.

(k) `notifyTranscriptReady`를 교체:

```js
/** 후처리 완료 토스트. prompt.txt를 우선 열고, 전사 문서가 있으면 함께 안내한다. */
function notifyTranscriptReady(job) {
  const state = ctx.getState();
  const canOpen = state.mode === 'folder' && ctx.openFile;
  const lines = [`prompt.txt 생성: ${job.promptRel || job.prompt || ''}`];
  if (job.transcript) lines.push(`전사 문서: ${job.transcriptRel || job.transcript}`);
  if (canOpen && job.promptRel) {
    showToast(lines.join('\n'), {
      action: { label: 'prompt.txt 열기', run: () => ctx.openFile(job.promptRel) },
    });
  } else {
    showToast(lines.join('\n'));
  }
}
```

(l) `function showToast(` → `export function showToast(`.

- [ ] **Step 8: webopen.js를 작성한다**

```js
// webopen.js — 툴바 「웹 열기」 Worker
//
// 입력한 URL을 서버(/api/web/open)에 보내 전용 Chrome 창에서 열게 한다.
// 녹음 중이면 recorder.js에 web-open 이벤트를 남긴다. 마지막 URL은 브라우저에 기억한다.

import { normalizeWebURL } from './web-url.js';
import { noteWebOpen, showToast } from './recorder.js';

const API_WEB_OPEN_PATH = '/api/web/open';
const URL_STORE_KEY = 'mdview-web-url';
const ERROR_TEXT_LIMIT = 200;

let input = null;
let button = null;

export function initWebOpen() {
  input = document.getElementById('web-url');
  button = document.getElementById('web-open');
  if (!input || !button) return;

  try {
    const saved = localStorage.getItem(URL_STORE_KEY);
    if (saved) input.value = saved;
  } catch {
    /* localStorage 불가 환경 무시 */
  }

  button.addEventListener('click', openFromInput);
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      openFromInput();
    }
  });
}

async function openFromInput() {
  const url = normalizeWebURL(input.value);
  if (!url) {
    showToast('http/https 주소를 입력하세요 (예: https://example.com)', { error: true });
    input.focus();
    return;
  }
  button.disabled = true;
  try {
    const res = await fetch(API_WEB_OPEN_PATH, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url }),
    });
    if (!res.ok) {
      const detail = (await res.text()).slice(0, ERROR_TEXT_LIMIT);
      throw new Error(detail || res.statusText);
    }
    const result = await res.json();
    try {
      localStorage.setItem(URL_STORE_KEY, url);
    } catch {
      /* 무시 */
    }
    noteWebOpen(url);
    showToast(result.launched
      ? `전용 Chrome 창을 열었습니다: ${url}\n녹음 중 그 창에서의 클릭·더블클릭·복사가 함께 기록됩니다.`
      : `새 탭으로 열었습니다: ${url}`);
  } catch (err) {
    showToast(`웹 페이지를 열지 못했습니다: ${err.message}`, { error: true });
  } finally {
    button.disabled = false;
  }
}
```

- [ ] **Step 9: app.js에 배선한다**

import 줄 추가: `import { initWebOpen } from './webopen.js';`
`bootstrap()`에서 `initRecorder({ … });` 호출 직후에 `initWebOpen();` 추가.

- [ ] **Step 10: 빌드하고 수동으로 화면을 확인한다**

```bash
./install.sh --build-only && ./mdview -port 8699 README.md
```

브라우저에서 툴바에 입력창과 「웹 열기」 버튼이 보이고, 빈 값으로 누르면 안내 토스트가 뜨는지 확인한다. `Ctrl+C`로 종료.

- [ ] **Step 11: Commit**

```bash
git add internal/mdview/static test/web-url.test.mjs
git commit -m "feat(mdview): 툴바 웹 열기 입력과 녹음 중 웹 이벤트 연동·후처리 토스트

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 9: 문서, 수동 E2E, 설치

**Files:**
- Modify: `README.md`, `docs/specs/2026-10-18-web-capture-design.md`

- [ ] **Step 1: 수동 E2E를 실행한다** (실제 Chrome 필요)

```bash
./install.sh --build-only
mkdir -p /tmp/mdview-e2e && cd /tmp/mdview-e2e && printf '# 테스트\n\n본문 한 줄\n' > doc.md
DEBUG_MODE=true /Users/ryu/projects/personal/tools.camp/cli/mdview/mdview
```

순서: ① 툴바에 `https://example.com` 입력 → 「웹 열기」 → 전용 Chrome 창이 뜨는지. ② ⏺ 녹음 시작 → doc.md 본문 클릭 → Chrome 창에서 제목 클릭·단어 더블클릭 → 칩이 `이벤트 N · 웹 M`으로 바뀌는지. ③ 「웹 열기」로 다른 URL을 한 번 더 열어 새 탭으로 열리는지. ④ 정지 → 토스트에 prompt.txt 안내 → 「prompt.txt 열기」.

확인:

```bash
cat mdview-recordings/rec-*/events.json | head -60   # focus 와 web-click 이 t 순서로 섞여 있는지, web-open 이 있는지
cat mdview-recordings/rec-*/prompt.txt               # 문서 조작 줄과 "웹 클릭" 줄 + url/맥락
```

⑤ Chrome 창을 모두 닫은 뒤 다시 「웹 열기」 → 재기동되는지. ⑥ mdview `Ctrl+C` → Chrome도 닫히는지.

결과(통과/실패, 관찰)를 최종 보고에 적는다. 실패하면 로그(`logs/` 세션 디렉토리, `[WebCapture]`/`[WebOpener]`)를 보고 superpowers:systematic-debugging으로 원인을 찾는다.

- [ ] **Step 2: README.md를 갱신한다**

(a) 「기능」 목록의 **오디오 녹화** 항목 뒤에 추가:

```markdown
- **웹 페이지 함께 녹화**: 툴바에 URL을 넣고 「웹 열기」를 누르면 전용 프로필의 Chrome 창이 그 사이트를 엽니다. 녹음 중 그 창에서 한 클릭·더블클릭·복사가 요소 정보(CSS 경로, 태그, 텍스트, 상위 제목, 주변 텍스트)와 함께 문서 이벤트와 **같은 타임라인**에 기록됩니다. Chrome이 필요합니다(`MDVIEW_CHROME_PATH`로 경로 지정 가능).
- **프롬프트 생성**: 녹화 저장 후 발화와 문서·웹 조작을 시간순 텍스트로 정리한 `prompt.txt`가 항상 만들어집니다(API 키가 없으면 조작 기록만으로). 완료 토스트의 「prompt.txt 열기」로 바로 볼 수 있고, 그 내용을 AI에게 붙여 넣으면 됩니다.
```

(b) 「오디오 녹화 및 전사」 절의 「녹화」 2번 항목 목록 끝에 추가:

```markdown
   - **web-open / web-click / web-dblclick / web-copy**: 「웹 열기」로 연 Chrome 창에서의 조작 — `url`, `title`, `selector`, `tag`, `id`, `classes`, `text`, `heading`, `context`, `selection`
```

`events.json` 예시의 events 배열 끝에 추가:

```json
    { "t": 16000, "type": "web-open", "url": "https://example.com/dashboard" },
    { "t": 18200, "type": "web-click", "url": "https://example.com/dashboard?tab=metrics", "selector": "main > span.metric-value", "tag": "span", "classes": "metric-value", "text": "12,345", "heading": "핵심 지표", "context": "핵심 지표 월간 활성 사용자 12,345" }
```

(c) 「녹화」 3번 산출물 목록에 `prompt.txt — 발화·조작 타임라인 텍스트 (AI에게 붙여 넣는 용도, 항상 생성)` 추가.

(d) 「전사」 절의 첫 문단을 다음으로 바꾼다:

```markdown
**자동 후처리**: 녹화 저장 직후 서버가 백그라운드에서 전사(API 키가 있고 25MB 이하일 때)와 `prompt.txt` 생성을 진행합니다.
툴바에 "후처리 중" 칩이 표시되고, 완료되면 토스트의 「prompt.txt 열기」로 바로 볼 수 있습니다.
(키가 없거나 오디오가 25MB를 넘으면 전사만 생략되고 `prompt.txt`는 조작 기록만으로 만들어집니다. 상태 API: `GET /api/recording/status`)
```

수동 실행 설명에 `mdview transcribe`가 prompt.txt도 다시 만든다는 문장을 추가한다.

(e) 「웹 페이지 함께 녹화」 절을 「전사」 절 앞에 신설:

```markdown
### 웹 페이지 함께 녹화

1. 툴바의 입력창에 URL을 넣고 「웹 열기」(또는 Enter). scheme을 생략하면 `https://`가 붙습니다.
2. 전용 프로필(`~/.config/mdview/chrome-profile`)의 Chrome 창이 열립니다. 로그인이 필요한 사이트는 이 프로필에서 한 번 로그인하면 유지됩니다.
3. 녹음 중 그 창(새 탭·팝업 포함)에서의 클릭·더블클릭·복사가 기록됩니다. 툴바 칩에 `이벤트 N · 웹 M`으로 수집 수가 보입니다.
4. 같은 세션에서 다시 「웹 열기」를 누르면 새 탭으로 열립니다. Chrome 창을 모두 닫으면 다음 「웹 열기」에서 다시 기동합니다. mdview를 끝내면 Chrome도 함께 닫힙니다.

알려진 제한 (webview와 동일):
- 교차 출처 iframe 내부의 조작은 기록되지 않습니다.
- `navigator.clipboard.writeText()`를 쓰는 복사 버튼은 `copy`가 아니라 그 버튼의 `click`으로만 기록됩니다.
- 새 탭이 열린 직후 스크립트 주입 전까지 짧은 유실 구간이 있습니다.
- 이 프로필로 로그인한 세션은 DevTools 포트를 통해 로컬의 다른 프로세스가 접근할 수 있습니다. 민감한 계정 로그인은 피하세요.
- 입력란 값·비밀번호 주변 텍스트·클릭 좌표·스크린샷은 수집하지 않습니다.
```

(f) 「프로젝트 구조」에 새 파일을 추가:

```
      websession.go            # 웹 조작 이벤트 검증·버퍼 Service
      webopen.go               # URL 검증 + 전용 Chrome 수명 소유
      webbrowser.go            # 전용 프로필 Chrome 기동·탭 열기 Gateway
      webcapture.go            # 타깃 추적 + 격리 월드 주입 + 이벤트 수신 Worker
      chrome.go                # Chrome 실행 파일 탐색
      pipeline.go              # 저장 → 전사(가능 시) → prompt.txt Sub-Orchestrator
      prompt.go                # prompt.txt 생성 (순수 함수)
      inject/capture.js        # 대상 페이지에 주입되는 조작 수집 스크립트
      static/js/webopen.js     # 툴바 「웹 열기」 Worker
      static/js/web-url.js     # URL 정규화
```

(g) 「요구사항」을 갱신:

```markdown
- Go 1.26 이상 (cdproto 요구사항. 로컬 Go가 더 낮으면 `GOTOOLCHAIN=auto`로 첫 빌드 시 자동 다운로드)
- 웹 페이지 녹화를 쓰려면 Google Chrome 또는 Chromium (`MDVIEW_CHROME_PATH`로 경로 지정 가능). 없어도 나머지 기능은 동작
- macOS / Linux / Windows
```

- [ ] **Step 3: spec의 prompt.txt 예시를 구현과 맞춘다** — `docs/specs/2026-10-18-web-capture-design.md` 6.3절의 PDF 줄을 다음으로 교체:

```text
[00:10] 클릭: "클릭 지점 주변 텍스트" (docs/spec.pdf p.2 x=0.41 y=0.27)
```

그리고 근사 표기 예시 한 줄을 추가: `[00:22] 클릭: "근사 블록" (docs/guide.md:90-92, 근사)`. 머리말 형식도 `조작 N개 (문서 a · 웹 b)` 와 `대상:` 줄을 반영한다.

- [ ] **Step 4: 전체 검증**

```bash
go test ./... && go vet ./... && node --test test/*.test.mjs   # Node 22는 디렉토리 인자를 모듈로 취급하므로 glob 사용
```

Expected: 모두 통과.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/specs/2026-10-18-web-capture-design.md
git commit -m "docs(mdview): 웹 페이지 녹화·prompt.txt 사용법과 제한 사항 문서화

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

- [ ] **Step 6: 설치 (사용자 바이너리 교체)**

실행 중인 mdview가 있으면 종료한 뒤 `./install.sh`. in-place 덮어쓰기로 SIGKILL이 나면 `rm ~/.local/bin/mdview && cp mdview ~/.local/bin/ && codesign -s - ~/.local/bin/mdview` (메모리 `deploy-go-cli-codesign` 참고). `which mdview`가 `~/.local/bin/mdview`인지 확인한다(구버전 `~/bin` 섀도잉 주의).

---

## Self-Review 결과

- **Spec 커버리지**: §3 구조(T6·T7), §4 파일(전부 Task에 배정), §5.1 열기(T7·T8), §5.2 시간 기준(T8 d·e, T2), §5.3 병합(T5), §5.4 파이프라인(T4·T5·T7 main), §6 스키마(T1·T2·T3), §7 API(T5·T7), §8 Chrome 수명(T7 WebOpener), §9 프론트(T8), §10 오류 처리(T5·T7 핸들러 분기, T8 토스트), §11 테스트(각 Task), §12 제한(T9 README).
- **타입 일관성**: `WebSession.Add(string)` ↔ `NewWebOpener(s.web.Add)`; `RecordingEvent` 웹 필드명은 T1·T2·T3·T5에서 동일; `TranscribeJob.Prompt/PromptRel` T1 정의 → T5 사용 → T8 `job.promptRel`; `apiWeb*Path` 상수 T5 정의 → T7 open 추가 → T8 JS 경로 문자열 일치; `ErrChromeUnavailable`은 T6 chrome.go(복사본)에서 옴.
- **자리표시자 없음** 확인.
