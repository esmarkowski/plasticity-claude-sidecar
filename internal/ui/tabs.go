package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"claude-sidecar/internal/attrib"
	"claude-sidecar/internal/event"
	"claude-sidecar/internal/transcript"
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

// hooksView is the tab that catches misconfiguration.
//
// Failures come first and in red, because a broken hook is the one thing here
// that is actionable and the one thing Claude Code will not otherwise tell you:
// a non-zero exit is reported as a "non-blocking" error, which means the session
// carries on and the only trace is a line in a transcript nobody reads.
func (m Model) hooksView(w int) string {
	var b strings.Builder

	failing := groupFailures(m.hooks)
	if len(failing) > 0 {
		b.WriteString(badStyle.Render(fmt.Sprintf("✗ %d hook%s failing", len(failing), plural(len(failing)))) + "\n\n")
		for _, f := range failing {
			b.WriteString("  " + badStyle.Render(pad(trunc(f.Name, 30), 31)) +
				dimStyle.Render(pad("exit "+fmt.Sprint(f.ExitCode), 9)) +
				faintStyle.Render(fmt.Sprintf("×%d", f.Count)) + "\n")
			if f.Command != "" {
				b.WriteString("    " + faintStyle.Render(trunc(f.Command, maxInt(w-6, 20))) + "\n")
			}
			if f.Stderr != "" {
				b.WriteString("    " + warnStyle.Render(trunc(cleanStderr(f.Stderr), maxInt(w-6, 20))) + "\n")
			}
		}
		b.WriteString("\n" + faintStyle.Render(
			"exit 127 usually means PATH: hooks run under /bin/sh with no shell activation, "+
				"so an interpreter needs an absolute path.") + "\n\n")
	} else if len(m.hooks) > 0 {
		b.WriteString(goodStyle.Render("✓ no failing hooks") + "\n\n")
	}

	// Hooks are allowed to add context, and one that adds a page of
	// instructions on every session start is a recurring cost worth naming.
	if inj := injections(m.hooks); len(inj) > 0 {
		b.WriteString(titleStyle.Render("Injecting context") + "\n")
		for _, f := range inj {
			b.WriteString("  " + pad(trunc(f.Name, 30), 31) +
				padLeft(numStyle.Render(comma(f.Tokens)), 8) + dimStyle.Render(" tok") +
				faintStyle.Render(fmt.Sprintf("  ×%d", f.Count)) + "\n")
		}
		b.WriteString("\n")
	}

	// Timing is sourced from the transcript's own hook records, not from the
	// duration_ms on a PostToolUse payload — that is how long the *tool* took,
	// and reporting it as hook latency would be confidently wrong.
	if timings := hookTimings(m.hooks); len(timings) > 0 {
		b.WriteString(titleStyle.Render("Latency") + dimStyle.Render("  — a hook is on the critical path of its turn") + "\n")
		b.WriteString(faintStyle.Render(pad("hook", 40)+padLeft("ran", 6)+padLeft("worst", 10)+padLeft("mean", 9)) + "\n")
		for _, t := range timings {
			style := dimStyle
			if t.Worst > time.Second {
				style = badStyle
			} else if t.Worst > 200*time.Millisecond {
				style = warnStyle
			}
			b.WriteString(pad(trunc(t.Name, 39), 40) +
				padLeft(numStyle.Render(fmt.Sprint(t.Count)), 6) +
				padLeft(style.Render(t.Worst.String()), 10) +
				padLeft(dimStyle.Render(t.Mean().String()), 9) + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(titleStyle.Render("Activity") + dimStyle.Render("  — events the sidecar recorded") + "\n")
	rows := hookActivity(m.events, m.current.ID)
	if len(rows) == 0 {
		b.WriteString(dimStyle.Render("no hook events for this session yet") + "\n")
	} else {
		b.WriteString(faintStyle.Render(pad("event", 26)+padLeft("fired", 8)) + "\n")
		for _, r := range rows {
			b.WriteString(pad(r.name, 26) + padLeft(numStyle.Render(fmt.Sprint(r.count)), 8) + "\n")
		}
	}

	b.WriteString("\n" + titleStyle.Render("Recent") + "\n")
	recent := m.events
	if len(recent) > 10 {
		recent = recent[len(recent)-10:]
	}
	for i := len(recent) - 1; i >= 0; i-- {
		ev := recent[i]
		b.WriteString(dimStyle.Render(ev.TS.Local().Format("15:04:05")) + "  " +
			pad(ev.Event, 22) + faintStyle.Render(trunc(ev.Summary(), maxInt(w-34, 10))) + "\n")
	}
	return b.String()
}

// timing aggregates one hook's measured durations.
type timing struct {
	Name  string
	Count int
	Worst time.Duration
	Total time.Duration
}

func (t timing) Mean() time.Duration {
	if t.Count == 0 {
		return 0
	}
	return (t.Total / time.Duration(t.Count)).Round(time.Millisecond)
}

func hookTimings(hooks []transcript.HookRun) []timing {
	byName := map[string]*timing{}
	var order []string
	for _, h := range hooks {
		if h.DurationMS <= 0 {
			continue
		}
		// The attachments carry a human-facing description; the stop summary
		// carries the real command. Prefer whichever identifies the hook.
		name := h.Name
		if name == "" {
			name = h.Command
		}
		if name == "" {
			continue
		}
		t, ok := byName[name]
		if !ok {
			t = &timing{Name: name}
			byName[name] = t
			order = append(order, name)
		}
		d := time.Duration(h.DurationMS) * time.Millisecond
		t.Count++
		t.Total += d
		if d > t.Worst {
			t.Worst = d
		}
	}
	out := make([]timing, 0, len(order))
	for _, k := range order {
		out = append(out, *byName[k])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Worst > out[j].Worst })
	return out
}

// failure is a distinct hook failure, counted rather than listed: the same
// broken hook fires on every turn and would otherwise bury everything else.
type failure struct {
	Name     string
	Command  string
	Stderr   string
	ExitCode int
	Count    int
}

func groupFailures(hooks []transcript.HookRun) []failure {
	byKey := map[string]*failure{}
	var order []string
	for _, h := range hooks {
		if !h.Failed {
			continue
		}
		key := h.Name + "\x00" + fmt.Sprint(h.ExitCode)
		f, ok := byKey[key]
		if !ok {
			f = &failure{Name: h.Name, Command: h.Command, Stderr: h.Stderr, ExitCode: h.ExitCode}
			byKey[key] = f
			order = append(order, key)
		}
		f.Count++
	}
	out := make([]failure, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

// injection is a hook that adds text to the context window.
type injection struct {
	Name   string
	Tokens int
	Count  int
}

func injections(hooks []transcript.HookRun) []injection {
	byName := map[string]*injection{}
	var order []string
	for _, h := range hooks {
		if h.Injected == "" {
			continue
		}
		in, ok := byName[h.Name]
		if !ok {
			in = &injection{Name: h.Name}
			byName[h.Name] = in
			order = append(order, h.Name)
		}
		in.Count++
		in.Tokens += attrib.Estimate(h.Injected)
	}
	out := make([]injection, 0, len(order))
	for _, k := range order {
		out = append(out, *byName[k])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tokens > out[j].Tokens })
	return out
}

type activityRow struct {
	name  string
	count int
}

func hookActivity(events []event.Event, session string) []activityRow {
	byName := map[string]*activityRow{}
	var order []string
	for _, ev := range events {
		if session != "" && ev.Session != "" && ev.Session != session {
			continue
		}
		r, ok := byName[ev.Event]
		if !ok {
			r = &activityRow{name: ev.Event}
			byName[ev.Event] = r
			order = append(order, ev.Event)
		}
		r.count++
	}
	out := make([]activityRow, 0, len(order))
	for _, k := range order {
		out = append(out, *byName[k])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].count > out[j].count })
	return out
}

// cleanStderr drops the wrapper Claude Code adds so the actual error leads.
func cleanStderr(s string) string {
	const prefix = "Failed with non-blocking status code: "
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), prefix))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
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
