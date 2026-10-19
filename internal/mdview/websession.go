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
	maxWebPayloadBytes  = 8 << 10 // binding payload 원문 상한
	maxWebSessionEvents = 5000    // 도달하면 추가 수집을 중단한다
	dblclickMergeMs     = 700     // 더블클릭 앞의 click을 같은 동작으로 볼 시간 창
	dblclickScanBack    = 6       // 버퍼 끝에서 훑을 개수 (탭 교차로 인접이 보장되지 않는다)
	dblclickMaxMerge    = 2       // 더블클릭 1회가 만드는 click 수

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
	s.freezeLocked(durationMs)
	return len(s.events)
}

// Snapshot은 수집을 멈추고 t를 [0, durationMs]로 클램프한 뒤 버퍼의 복사본을 돌려준다 (버퍼는 유지).
// 업로드 저장이 실패해도 재시도 업로드가 같은 이벤트를 다시 병합할 수 있어야 하기 때문이다.
// durationMs가 0 이하(meta에 녹화 길이가 없음)면 상한 클램프는 건너뛴다. 모든 t가 0으로 뭉개지지 않게 하기 위해서다.
func (s *WebSession) Snapshot(durationMs int64) (events []RecordingEvent, dropped int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if durationMs > 0 {
		s.freezeLocked(durationMs)
	} else {
		s.recording = false
		for i := range s.events {
			if s.events[i].T < 0 {
				s.events[i].T = 0
			}
		}
	}
	return append([]RecordingEvent(nil), s.events...), s.dropped
}

// Clear는 저장이 성공한 뒤 버퍼를 비운다 (수집도 멈춘 상태로 둔다).
func (s *WebSession) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recording = false
	s.events = nil
	s.dropped = 0
	s.limitWarned = false
}

// freezeLocked는 수집을 멈추고 모든 t를 [0, durationMs]로 클램프한다.
func (s *WebSession) freezeLocked(durationMs int64) {
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
}

// StartEpochMs는 현재 세션의 녹음 시작 시각(epoch ms)을 돌려준다.
func (s *WebSession) StartEpochMs() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startEpochMs
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

// clampEpochLocked는 위조·시계 조정으로 범위를 벗어난 시각을 녹음 구간 안으로 되돌린다.
func (s *WebSession) clampEpochLocked(atEpochMs int64) int64 {
	now := time.Now().UnixMilli()
	upper := now
	if upper < s.startEpochMs {
		upper = s.startEpochMs
	}
	if atEpochMs < s.startEpochMs {
		return s.startEpochMs
	}
	if atEpochMs > upper {
		return upper
	}
	return atEpochMs
}

// mergeDblClickLocked는 더블클릭이 만든 선행 click을 제거한다.
// 이벤트는 타깃별 채널로 오므로 다른 탭의 click이 끼어들 수 있어 버퍼 끝에서 몇 개를 훑는다.
// 알려진 한계: 더블클릭이 자신의 click보다 먼저 도착하면 (타깃별 채널이 달라) 병합되지 않는다.
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

// isDblClickSource는 주어진 이벤트가 이 더블클릭이 만든 click인지 판별한다.
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
