package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"claude-sidecar/internal/attrib"
)

// rulesView answers "what instructions am I paying for, and why were they
// loaded". This is the tab the InstructionsLoaded hook exists for: none of this
// text appears in the transcript, so without the hook it is invisible.
func (m Model) rulesView(w int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Instruction files in context") + "\n\n")
	b.WriteString(bucketDetail(m.report, attrib.BucketRules, w, true))

	// Glob-matched rules are re-injected every time a matching file is touched,
	// and each injection is paid for again. A high count with a broad glob is
	// the most common quiet context leak.
	var repeats []string
	for _, s := range m.report.Slices {
		if s.Bucket != attrib.BucketRules {
			continue
		}
		for _, it := range s.Detail {
			if it.Count > 2 {
				repeats = append(repeats, fmt.Sprintf("%s re-injected %d times", truncPath(it.Name, 48), it.Count))
			}
		}
	}
	if len(repeats) > 0 {
		b.WriteString("\n\n" + warnStyle.Render("re-injected by glob match") + "\n")
		for _, r := range repeats {
			b.WriteString("  " + dimStyle.Render(r) + "\n")
		}
		b.WriteString(faintStyle.Render("  a rule whose globs are broad is charged again on every match"))
	}
	return b.String()
}

// toolsView ranks tools by what their results cost. Tool results are usually the
// largest single category, and they are the one category the user controls
// directly by choosing what to read.
func (m Model) toolsView(w int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Tool results") + dimStyle.Render("  — what came back into context") + "\n\n")
	b.WriteString(bucketDetail(m.report, attrib.BucketToolResults, w, false))
	b.WriteString("\n\n" + titleStyle.Render("Tool calls") + dimStyle.Render("  — what we sent") + "\n\n")
	b.WriteString(bucketDetail(m.report, attrib.BucketToolCalls, w, false))
	return b.String()
}

// agentsView shows each subagent's own context, broken down the same way as the
// parent's — a subagent has its own window and can fill it independently.
func (m Model) agentsView(w int) string {
	if len(m.agents) == 0 {
		return dimStyle.Render("no subagents in this session")
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Subagents") + "\n\n")
	b.WriteString(faintStyle.Render(fmt.Sprintf("%s%s%s%s%s",
		pad("type", 28), pad("state", 10), padLeft("context", 10), padLeft("reqs", 7), padLeft("elapsed", 10))) + "\n")

	for _, a := range m.agents {
		state := dimStyle.Render("done")
		if a.Running {
			state = goodStyle.Render("running")
		}
		fmt.Fprintf(&b, "%s%s%s%s%s\n",
			pad(trunc(a.Type, 27), 28),
			pad(state, 10),
			padLeft(numStyle.Render(comma(a.Report.Total)), 10),
			padLeft(dimStyle.Render(fmt.Sprint(a.Requests)), 7),
			padLeft(dimStyle.Render(a.Elapsed().String()), 10),
		)
		// One inline bar per agent: enough to see a subagent that is filling its
		// window on tool results without leaving the list.
		if a.Report.Total > 0 {
			b.WriteString("  " + stackedBar(a.Report.Slices, a.Report.Total, maxInt(w-4, 10)) + "\n")
		}
		if a.ReplySize > 0 {
			b.WriteString("  " + faintStyle.Render(fmt.Sprintf("returned %s of text to the parent", byteSize(a.ReplySize))) + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// hooksView is the tab that catches misconfiguration. Every hook firing is
// recorded with its duration and exit code, so a hook that fails silently —
// which is otherwise only visible as a stray line in the transcript — shows up
// as a red row.
func (m Model) hooksView(w int) string {
	type row struct {
		name  string
		count int
		fails int
		slow  time.Duration
	}
	byName := map[string]*row{}
	var order []string
	for _, ev := range m.events {
		if ev.Session != "" && m.current.ID != "" && ev.Session != m.current.ID {
			continue
		}
		r, ok := byName[ev.Event]
		if !ok {
			r = &row{name: ev.Event}
			byName[ev.Event] = r
			order = append(order, ev.Event)
		}
		r.count++
		if ev.Event == "PostToolUseFailure" || ev.Event == "StopFailure" || ev.Event == "unparseable" {
			r.fails++
		}
		if ms, ok := ev.Detail["duration_ms"].(float64); ok {
			if d := time.Duration(ms) * time.Millisecond; d > r.slow {
				r.slow = d
			}
		}
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("Hook activity") + dimStyle.Render("  — this session") + "\n\n")
	if len(order) == 0 {
		b.WriteString(dimStyle.Render("no hook events for this session yet"))
	} else {
		b.WriteString(faintStyle.Render(pad("event", 28)+padLeft("fired", 8)+padLeft("failed", 9)+padLeft("slowest", 11)) + "\n")
		for _, n := range order {
			r := byName[n]
			fails := dimStyle.Render("—")
			if r.fails > 0 {
				fails = badStyle.Render(fmt.Sprint(r.fails))
			}
			slow := dimStyle.Render("—")
			if r.slow > 0 {
				slow = dimStyle.Render(r.slow.String())
			}
			fmt.Fprintf(&b, "%s%s%s%s\n",
				pad(n, 28), padLeft(numStyle.Render(fmt.Sprint(r.count)), 8),
				padLeft(fails, 9), padLeft(slow, 11))
		}
	}

	b.WriteString("\n" + titleStyle.Render("Recent") + "\n")
	recent := m.events
	if len(recent) > 12 {
		recent = recent[len(recent)-12:]
	}
	for i := len(recent) - 1; i >= 0; i-- {
		ev := recent[i]
		b.WriteString(dimStyle.Render(ev.TS.Local().Format("15:04:05")) + "  " +
			pad(ev.Event, 22) + faintStyle.Render(trunc(ev.String(), maxInt(w-34, 10))) + "\n")
	}
	return b.String()
}

// timelineView is the per-request history: where the window went, and how well
// the cache is holding.
func (m Model) timelineView(w int) string {
	if len(m.audit) == 0 {
		return dimStyle.Render("no requests yet")
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Context growth per request") + "\n\n")
	b.WriteString(faintStyle.Render(pad("req", 6)+padLeft("context", 11)+padLeft("Δ", 9)+
		padLeft("measured", 11)+padLeft("residual", 11)+padLeft("thinking", 10)) + "\n")

	points := m.audit
	prev := 0
	for i, p := range points {
		delta := ""
		if i > 0 {
			d := p.Context - prev
			style := dimStyle
			if d > 10_000 {
				style = badStyle
			} else if d > 3_000 {
				style = warnStyle
			}
			delta = style.Render(fmt.Sprintf("+%s", comma(d)))
		}
		fmt.Fprintf(&b, "%s%s%s%s%s%s\n",
			pad(fmt.Sprint(p.Request), 6),
			padLeft(numStyle.Render(comma(p.Context)), 11),
			padLeft(delta, 9),
			padLeft(dimStyle.Render(comma(p.Measured)), 11),
			padLeft(dimStyle.Render(comma(p.Residual)), 11),
			padLeft(dimStyle.Render(comma(p.Thinking)), 10),
		)
		prev = p.Context
	}
	b.WriteString("\n" + faintStyle.Render(
		"residual is context minus everything measurable. If it climbs steadily, a category is being missed."))
	return b.String()
}

// pickerView is the session switcher. Sessions are identified by directory
// rather than uuid, because that is how anyone actually thinks about them.
func (m Model) pickerView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Switch session") + "\n\n")
	for i, s := range m.sessions {
		line := fmt.Sprintf("%s  %s", pad(s.Label(), 40),
			dimStyle.Render(s.Active.Local().Format("15:04:05")))
		if i == m.pick {
			b.WriteString(chipOn.Render("▸") + " " + titleStyle.Render(line) + "\n")
		} else {
			b.WriteString("  " + dimStyle.Render(line) + "\n")
		}
		if i > 14 {
			break
		}
	}
	b.WriteString("\n" + helpStyle.Render("enter select · esc cancel · selecting pins the session"))
	return panel.BorderForeground(accent).Render(b.String())
}

func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

var _ = lipgloss.JoinVertical
