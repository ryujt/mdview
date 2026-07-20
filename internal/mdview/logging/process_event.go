// Package logging은 mdview의 2계층 로깅 시스템이다.
//
// 1계층: 콘솔+파일 동시 기록하는 한 줄 텍스트 로그 (Debug/Info/Warn/Error)
// 2계층: 한 줄로 담을 수 없는 구조화 데이터를 카테고리별 파일로 저장
//   - whisper/   전사(whisper-1 API / 로컬 CLI) 호출 상세
//   - recording/ 녹화 업로드 저장 상세
//   - state/     상태 스냅샷 (JSON)
//   - process/   세션 프로세스 플로우 다이어그램 (Mermaid)
package logging

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// ProcessEvent는 파이프라인의 단일 단계를 기록한다.
// 모든 이벤트는 오케스트레이터 관점에서 기록된다 — 워커가 자기 자신의 진행을
// 기록하지 않고, 워커의 결과를 받아 다음 단계로 보내는 쪽이 기록한다.
type ProcessEvent struct {
	Timestamp time.Time
	Phase     string // "start" | "strategy" | "execution" | "synthesis" | "decision" | "end"
	EventType string // "start" | "end" | "step_ok" | "step_err" | "branch_ok" | "branch_fail" | "error"
	NodeLabel string

	ArtifactPath string  // 상세 로그 파일 경로 (다이어그램 노드에서 click 링크)
	Iteration    int     // fan-out/fan-in 그룹 번호
	DurationMs   float64 // 참고용 소요 시간
}

// EventOpts는 LogProcessEvent의 선택 메타데이터이다.
type EventOpts struct {
	ArtifactPath string
	Iteration    int
	DurationMs   float64
}

const (
	labelMaxLen = 80
	nodePrefix  = "N"
)

// MermaidRenderer는 ProcessEvent 목록을 Mermaid flowchart 마크다운 문서로 변환한다.
type MermaidRenderer struct{}

// sanitizeLabel은 Mermaid 레이블에 쓸 수 없는 특수문자를 안전한 문자로 치환한다.
func sanitizeLabel(text string) string {
	r := strings.NewReplacer(
		`"`, "'",
		"(", "[", ")", "]",
		"{", "[", "}", "]",
		"|", "/",
		"#", "No.",
		";", ",",
		"<", "&lt;", ">", "&gt;",
		"\n", "<br/>",
	)
	out := r.Replace(text)
	runes := []rune(out)
	if len(runes) > labelMaxLen {
		out = string(runes[:labelMaxLen]) + "..."
	}
	return out
}

// nodeShape는 phase에 따른 Mermaid 노드 형태 문자열을 반환한다.
func nodeShape(phase, label string) string {
	switch phase {
	case "start", "end":
		return fmt.Sprintf(`(["%s"])`, label)
	case "decision":
		return fmt.Sprintf(`{"%s"}`, label)
	default:
		return fmt.Sprintf(`["%s"]`, label)
	}
}

// resolveCSSClass는 이벤트의 시각 스타일 클래스를 결정한다.
func resolveCSSClass(ev ProcessEvent) string {
	switch ev.Phase {
	case "start":
		return "start"
	case "strategy":
		return "strategy"
	case "execution":
		if ev.EventType == "step_ok" {
			return "step_ok"
		}
		return "step_err"
	case "synthesis":
		return "synthesis"
	case "decision":
		return "decision"
	case "end":
		switch ev.EventType {
		case "cancel", "error", "branch_fail":
			return "end_fail"
		}
		return "end_ok"
	}
	return "start"
}

// Render는 이벤트 목록을 세션 메타 + Mermaid 다이어그램 + 아티팩트 목록을 담은
// 마크다운 문서로 렌더링한다. logDir는 아티팩트 상대 경로 계산 기준이다.
func (MermaidRenderer) Render(events []ProcessEvent, sessionID, logDir string) string {
	if len(events) == 0 {
		return "No process events recorded."
	}

	start := events[0].Timestamp
	end := events[len(events)-1].Timestamp
	duration := end.Sub(start).Seconds()

	nodeIDs := make([]string, len(events))
	for i := range events {
		nodeIDs[i] = fmt.Sprintf("%s%d", nodePrefix, i)
	}

	var lines []string
	lines = append(lines,
		"classDef start      fill:#2d9cdb,color:#fff;",
		"classDef strategy   fill:#8e44ad,color:#fff;",
		"classDef step_ok    fill:#27ae60,color:#fff;",
		"classDef step_err   fill:#e74c3c,color:#fff;",
		"classDef synthesis  fill:#3498db,color:#fff;",
		"classDef decision   fill:#e67e22,color:#fff;",
		"classDef end_ok     fill:#27ae60,color:#fff;",
		"classDef end_fail   fill:#e74c3c,color:#fff;",
		"",
	)

	for i, ev := range events {
		lines = append(lines, nodeIDs[i]+nodeShape(ev.Phase, sanitizeLabel(ev.NodeLabel)))
	}
	lines = append(lines, "")

	// --- 엣지 구성 (fan-out / fan-in) ---
	iterStrategy := map[int]string{}
	iterExec := map[int][]string{}
	for i, ev := range events {
		switch ev.Phase {
		case "strategy":
			iterStrategy[ev.Iteration] = nodeIDs[i]
		case "execution":
			iterExec[ev.Iteration] = append(iterExec[ev.Iteration], nodeIDs[i])
		}
	}

	var edgeLines []string
	connected := map[[3]string]bool{}
	addEdge := func(src, dst, label string) {
		if label != "" {
			// 동일 엣지의 무레이블 버전이 있으면 레이블 버전으로 교체한다.
			unlabeled := [3]string{src, dst, ""}
			if connected[unlabeled] {
				delete(connected, unlabeled)
				plain := src + " --> " + dst
				kept := edgeLines[:0]
				for _, l := range edgeLines {
					if l != plain {
						kept = append(kept, l)
					}
				}
				edgeLines = kept
			}
		}
		key := [3]string{src, dst, label}
		if connected[key] {
			return
		}
		connected[key] = true
		if label != "" {
			edgeLines = append(edgeLines, fmt.Sprintf(`%s -->|"%s"| %s`, src, label, dst))
		} else {
			edgeLines = append(edgeLines, src+" --> "+dst)
		}
	}

	prev := ""
	for i, ev := range events {
		nid := nodeIDs[i]
		switch ev.Phase {
		case "start":
			prev = nid
		case "strategy", "decision", "end":
			if prev != "" {
				addEdge(prev, nid, "")
			}
			prev = nid
		case "execution":
			// 같은 iteration의 strategy가 있을 때만 fan-out(병렬)로 그린다.
			// 전략 없는 실행 단계는 순차 체인으로 연결한다 (서버 기동 절차 등).
			if snid, ok := iterStrategy[ev.Iteration]; ok {
				addEdge(snid, nid, "")
			} else {
				if prev != "" {
					addEdge(prev, nid, "")
				}
				prev = nid
			}
		case "synthesis":
			_, hasStrategy := iterStrategy[ev.Iteration]
			if execs := iterExec[ev.Iteration]; hasStrategy && len(execs) > 0 {
				for _, en := range execs {
					addEdge(en, nid, "")
				}
			} else if prev != "" {
				addEdge(prev, nid, "")
			}
			prev = nid
		default:
			if prev != "" {
				addEdge(prev, nid, "")
			}
			prev = nid
		}
	}

	// decision 분기 엣지 후처리
	for i, ev := range events {
		if ev.Phase != "decision" {
			continue
		}
		switch ev.EventType {
		case "branch_ok":
			if next := findNextPhase(events, nodeIDs, i, "end"); next != "" {
				addEdge(nodeIDs[i], next, "threshold met")
			}
		case "branch_fail":
			if next := findNextPhase(events, nodeIDs, i, "strategy"); next != "" {
				addEdge(nodeIDs[i], next, "below threshold")
			}
		}
	}

	lines = append(lines, edgeLines...)
	lines = append(lines, "")

	for i, ev := range events {
		lines = append(lines, fmt.Sprintf("class %s %s;", nodeIDs[i], resolveCSSClass(ev)))
	}
	lines = append(lines, "")

	// click 링크 + 아티팩트 목록
	type artifactRow struct{ nid, etype, path string }
	var artifacts []artifactRow
	for i, ev := range events {
		if ev.ArtifactPath == "" {
			continue
		}
		rel := ev.ArtifactPath
		if logDir != "" {
			if r, err := filepath.Rel(logDir, ev.ArtifactPath); err == nil {
				rel = filepath.ToSlash(r)
			}
		}
		lines = append(lines, fmt.Sprintf(`click %s "%s" "Open artifact"`, nodeIDs[i], rel))
		artifacts = append(artifacts, artifactRow{nodeIDs[i], ev.EventType, rel})
	}

	// --- 마크다운 문서 조합 ---
	var doc strings.Builder
	doc.WriteString("# Process Flow\n\n")
	doc.WriteString("| 항목 | 값 |\n|------|-----|\n")
	doc.WriteString(fmt.Sprintf("| Session ID | `%s` |\n", orDash(sessionID)))
	doc.WriteString(fmt.Sprintf("| 시작 시각 | %s |\n", start.Format("2006-01-02 15:04:05")))
	doc.WriteString(fmt.Sprintf("| 종료 시각 | %s |\n", end.Format("2006-01-02 15:04:05")))
	doc.WriteString(fmt.Sprintf("| 소요 시간 | %.1fs |\n\n", duration))

	doc.WriteString("```mermaid\nflowchart TD\n")
	for _, l := range lines {
		if l == "" {
			doc.WriteString("\n")
		} else {
			doc.WriteString("    " + l + "\n")
		}
	}
	doc.WriteString("```\n")

	if len(artifacts) > 0 {
		doc.WriteString("\n## Artifacts\n\n| Node | Type | Path |\n|------|------|------|\n")
		for _, a := range artifacts {
			doc.WriteString(fmt.Sprintf("| %s | %s | `%s` |\n", a.nid, a.etype, a.path))
		}
	}
	return doc.String()
}

func findNextPhase(events []ProcessEvent, nodeIDs []string, after int, phase string) string {
	for i := after + 1; i < len(events); i++ {
		if events[i].Phase == phase {
			return nodeIDs[i]
		}
	}
	return ""
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
