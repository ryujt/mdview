// Package logging is mdview's two-tier logging system.
//
// Tier 1: one-line text logs written to both console and file (Debug/Info/Warn/Error)
// Tier 2: structured data that does not fit on one line, saved to per-category files
//   - whisper/   transcription call details (whisper-1 API / local CLI)
//   - recording/ recording upload details
//   - state/     state snapshots (JSON)
//   - process/   session process flow diagrams (Mermaid)
package logging

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// ProcessEvent records a single pipeline step.
// All events are recorded from the orchestrator's perspective: workers do not log
// their own progress; whoever receives a worker's result and dispatches the next step does.
type ProcessEvent struct {
	Timestamp time.Time
	Phase     string // "start" | "strategy" | "execution" | "synthesis" | "decision" | "end"
	EventType string // "start" | "end" | "step_ok" | "step_err" | "branch_ok" | "branch_fail" | "error"
	NodeLabel string

	ArtifactPath string  // detailed log file path (click link from the diagram node)
	Iteration    int     // fan-out/fan-in group number
	DurationMs   float64 // elapsed time, for reference
}

// EventOpts holds optional metadata for LogProcessEvent.
type EventOpts struct {
	ArtifactPath string
	Iteration    int
	DurationMs   float64
}

const (
	labelMaxLen = 80
	nodePrefix  = "N"
)

// MermaidRenderer converts a list of ProcessEvents into a Mermaid flowchart Markdown document.
type MermaidRenderer struct{}

// sanitizeLabel replaces characters not allowed in Mermaid labels with safe ones.
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

// nodeShape returns the Mermaid node shape string for the phase.
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

// resolveCSSClass determines the visual style class for an event.
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

// Render renders the events as a Markdown document containing session metadata,
// a Mermaid diagram and the artifact list. logDir is the base for relative artifact paths.
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

	// --- Build edges (fan-out / fan-in) ---
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
			// If an unlabeled version of the same edge exists, replace it with the labeled one.
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
			// Draw as fan-out (parallel) only when a strategy exists for the same iteration.
			// Execution steps without a strategy are chained sequentially (e.g. server startup).
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

	// Post-process decision branch edges
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

	// click links + artifact list
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

	// --- Assemble Markdown document ---
	var doc strings.Builder
	doc.WriteString("# Process Flow\n\n")
	doc.WriteString("| Field | Value |\n|------|-----|\n")
	doc.WriteString(fmt.Sprintf("| Session ID | `%s` |\n", orDash(sessionID)))
	doc.WriteString(fmt.Sprintf("| Start time | %s |\n", start.Format("2006-01-02 15:04:05")))
	doc.WriteString(fmt.Sprintf("| End time | %s |\n", end.Format("2006-01-02 15:04:05")))
	doc.WriteString(fmt.Sprintf("| Duration | %.1fs |\n\n", duration))

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
