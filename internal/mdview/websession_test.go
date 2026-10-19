package mdview

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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

// takeAll은 Snapshot(상한 클램프 없음)+Clear로 이벤트·폐기 건수·시작 시각을 회수하고 버퍼를 비운다.
func takeAll(s *WebSession) ([]RecordingEvent, int, int64) {
	start := s.StartEpochMs()
	events, dropped := s.Snapshot(0)
	s.Clear()
	return events, dropped, start
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

	events, dropped, start := takeAll(s)
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

func TestWebSessionTakeAllStopsCollecting(t *testing.T) {
	s := startedWebSession(t)
	s.Add(webClickPayload(testStartEpochMs+100, "div", "https://example.com"))
	if events, _, _ := takeAll(s); len(events) != 1 {
		t.Fatalf("이벤트 %d개, want 1개", len(events))
	}
	s.Add(webClickPayload(testStartEpochMs+200, "div", "https://example.com"))
	if recording, count := s.Live(); recording || count != 0 {
		t.Errorf("Snapshot·Clear 후 recording=%v events=%d, want false/0", recording, count)
	}
}

func TestWebSessionStartDiscardsPreviousBuffer(t *testing.T) {
	s := startedWebSession(t)
	s.Add(webClickPayload(testStartEpochMs+100, "div", "https://example.com"))
	s.Add(webPayload(map[string]any{"type": "bogus"}))
	s.Start(testStartEpochMs + 90000)
	events, dropped, start := takeAll(s)
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
	events, dropped, _ := takeAll(s)
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
	events, _, _ := takeAll(s)
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
	events, _, _ := takeAll(s)
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
	events, dropped, _ := takeAll(s)
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
	events, _, _ := takeAll(s)
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
	events, _, _ := takeAll(s)
	if events[1].T != 60000 {
		t.Errorf("durationMs를 넘은 t = %d, want 60000", events[1].T)
	}
}

// Snapshot은 수집을 멈추고 클램프된 복사본을 주되 버퍼는 남긴다. Clear가 비운다.
func TestWebSessionSnapshotKeepsBufferAndClear(t *testing.T) {
	s := startedWebSession(t)
	s.Add(webClickPayload(testStartEpochMs+500, "a", "https://example.com"))
	s.Add(webClickPayload(testStartEpochMs+90000, "b", "https://example.com"))
	s.Add(webPayload(map[string]any{"type": "bogus"}))

	events, dropped := s.Snapshot(60000)
	if len(events) != 2 || events[0].T != 500 || events[1].T != 60000 {
		t.Fatalf("Snapshot events = %+v, want t=500, 60000", events)
	}
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
	events[0].T = 999 // 복사본이어야 한다
	s.Add(webClickPayload(testStartEpochMs+700, "c", "https://example.com"))
	if recording, count := s.Live(); recording || count != 2 {
		t.Errorf("Snapshot 후 recording=%v events=%d, want false 2", recording, count)
	}
	again, _ := s.Snapshot(60000)
	if again[0].T != 500 {
		t.Errorf("반환값 수정이 버퍼에 반영됨: %+v", again[0])
	}

	s.Clear()
	if _, count := s.Live(); count != 0 {
		t.Errorf("Clear 후 events=%d", count)
	}
	if events, dropped := s.Snapshot(60000); len(events) != 0 || dropped != 0 {
		t.Errorf("Clear 후 Snapshot = %v, %d", events, dropped)
	}
}

// meta에 녹화 길이가 없으면(0) 상한 클램프 없이 음수만 0으로 맞춘다.
func TestWebSessionSnapshotWithoutDuration(t *testing.T) {
	s := startedWebSession(t)
	s.Add(webClickPayload(testStartEpochMs+1500, "a", "https://example.com"))
	events, _ := s.Snapshot(0)
	if len(events) != 1 || events[0].T != 1500 {
		t.Errorf("Snapshot(0) events = %+v, want t=1500", events)
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
	if got := truncateRunes("가나다", 3); got != "가나다" {
		t.Errorf("max 길이 문자열이 변경됨: %q", got)
	}
	if got := truncateRunes(" \t\n ", 3); got != "" {
		t.Errorf("공백 입력 = %q, want \"\"", got)
	}
}

func TestWebSessionConcurrentAddAndSnapshot(t *testing.T) {
	s := startedWebSession(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				s.Add(webClickPayload(testStartEpochMs+int64(i), fmt.Sprintf("div%d", i), "https://example.com"))
			}
		}(i)
	}
	for n := 0; n < 50; n++ {
		s.Live()
		s.Freeze(1000)
		s.Start(testStartEpochMs)
		takeAll(s)
	}
	wg.Wait()
}

func TestWebSessionDblClickMergeBoundaries(t *testing.T) {
	const sel, url = "p.para", "https://example.com/doc"
	dbl := func(at int64) string {
		return webPayload(map[string]any{"type": "dblclick", "atEpochMs": at, "selector": sel, "url": url})
	}
	t.Run("시간 창을 1ms 넘긴 click은 유지", func(t *testing.T) {
		s := startedWebSession(t)
		s.Add(webClickPayload(testStartEpochMs+1000, sel, url))
		s.Add(dbl(testStartEpochMs + 1000 + dblclickMergeMs + 1))
		events, _, _ := takeAll(s)
		if len(events) != 2 {
			t.Errorf("이벤트 %d개, want 2개: %+v", len(events), events)
		}
	})
	t.Run("일치 click 3개면 2개만 제거", func(t *testing.T) {
		s := startedWebSession(t)
		for _, at := range []int64{1000, 1100, 1200} {
			s.Add(webClickPayload(testStartEpochMs+at, sel, url))
		}
		s.Add(dbl(testStartEpochMs + 1210))
		events, _, _ := takeAll(s)
		if len(events) != 2 || events[0].Type != WebEventClick || events[1].Type != WebEventDblClick {
			t.Errorf("click 1 + dblclick 1이어야 함: %+v", events)
		}
	})
	t.Run("훑기 한도 밖의 click은 유지", func(t *testing.T) {
		s := startedWebSession(t)
		s.Add(webClickPayload(testStartEpochMs+1000, sel, url))
		for i := 0; i < dblclickScanBack; i++ {
			s.Add(webClickPayload(testStartEpochMs+1010+int64(i), fmt.Sprintf("div%d", i), url))
		}
		s.Add(dbl(testStartEpochMs + 1100))
		events, _, _ := takeAll(s)
		if len(events) != dblclickScanBack+2 || events[0].Selector != sel {
			t.Errorf("이벤트 %d개, want %d개: %+v", len(events), dblclickScanBack+2, events)
		}
	})
	t.Run("dblclick이 먼저 오면 병합하지 않음", func(t *testing.T) {
		s := startedWebSession(t)
		s.Add(dbl(testStartEpochMs + 1000))
		s.Add(webClickPayload(testStartEpochMs+1010, sel, url))
		s.Add(webClickPayload(testStartEpochMs+1020, sel, url))
		events, _, _ := takeAll(s)
		if len(events) != 3 {
			t.Errorf("이벤트 %d개, want 3개: %+v", len(events), events)
		}
	})
}

func TestWebSessionClampNeverNegative(t *testing.T) {
	s := NewWebSession()
	now := time.Now().UnixMilli()
	s.Start(now + 60_000)
	s.Add(webClickPayload(now+120_000, "div", "https://example.com")) // start(now+60s)보다 뒤이면서 now보다도 뒤: 수정 전 코드는 t=-60000
	events, _, _ := takeAll(s)
	if len(events) != 1 || events[0].T != 0 {
		t.Errorf("미래 시작 세션의 t = %+v, want 0", events)
	}
}
