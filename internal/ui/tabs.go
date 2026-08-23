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

// agentsView shows each subagent on one row: what it is, what it was asked to
// do, and how full its own window got. A subagent has its own context and can
// fill it independently, which is the thing worth seeing at a glance.
func (m Model) agentsView(w int) string {
	if len(m.agents) == 0 {
		return dimStyle.Render("no subagents in this session")
	}
	const (
		typeW    = 18
		stateW   = 9
		ctxW     = 10
		reqW     = 5
		elapsedW = 8
		replyW   = 9
		minBar   = 10
	)
	// The task description is the widest useful thing here and the first to be
	// squeezed; below that the bar goes, then the shorter columns.
	fixed := typeW + stateW + ctxW + reqW + elapsedW + replyW + 1
	taskW := minInt(maxInt(w-fixed-minBar, 0), 44)
	barW := maxInt(w-fixed-taskW, 6)

	// A subagent runs the same model as its parent, so it has the same window.
	// Without one, stackedGauge treats the total as the whole window and every
	// agent's bar reads as completely full regardless of size.
	window := m.report.Window
	if window == 0 {
		window = defaultWindow
	}

	var b strings.Builder
	missing := 0
	header := pad("agent", typeW)
	if taskW > 0 {
		header += pad("task", taskW)
	}
	header += pad("state", stateW) + padLeft("context", ctxW) +
		padLeft("reqs", reqW) + padLeft("elapsed", elapsedW) + padLeft("returned", replyW)
	b.WriteString(faintStyle.Render(header) + "\n")

	for _, a := range m.agents {
		state := dimStyle.Render("done")
		if a.Running {
			state = goodStyle.Render("running")
		}
		reply := dimStyle.Render("—")
		if a.ReplySize > 0 {
			reply = dimStyle.Render(byteSize(a.ReplySize))
		}

		ctx, reqs, bar := faintStyle.Render("—"), faintStyle.Render("—"), ""
		if a.Analyzed {
			ctx = numStyle.Render(comma(a.Report.Total))
			reqs = dimStyle.Render(fmt.Sprint(a.Requests))
			bar = stackedGauge(a.Report.Slices, a.Report.Total, window, barW)
		} else {
			missing++
			bar = faintStyle.Render(strings.Repeat("·", barW))
		}

		row := pad(trunc(a.Label(), typeW-1), typeW)
		if taskW > 0 {
			row += pad(dimStyle.Render(trunc(a.Task, taskW-1)), taskW)
		}
		row += pad(state, stateW) + padLeft(ctx, ctxW) + padLeft(reqs, reqW) +
			padLeft(dimStyle.Render(elapsedLabel(a)), elapsedW) + padLeft(reply, replyW)
		b.WriteString(row + " " + bar + "\n")
	}

	b.WriteString("\n" + faintStyle.Render(wrap(
		"bar length is how full that agent's own window is; colours match the context legend.", w)))
	if missing > 0 {
		// Worth saying, because the alternative reading — that these agents ran
		// with an empty context — would be wrong.
		b.WriteString("\n" + faintStyle.Render(wrap(fmt.Sprintf(
			"%d agent%s known only from hook events: no transcript under the session's "+
				"subagents/ directory, so neither its type nor its context could be read.",
			missing, plural(missing)), w)))
	}
	return b.String()
}

// hooksView is the tab that catches misconfiguration.
//
// Two columns: the activity roll on the left, which is a narrow list of counts,
// and on the right the two things worth reading — what is broken, and what is
// slow. Recent runs full width underneath because its rows are long.
func (m Model) hooksView(w int) string {
	// Widths, because lipgloss sizes a style by its content box plus padding but
	// not its border, and every one of these off by one shows up as a wrapped
	// table row rather than as anything obviously wrong.
	const (
		leftBox  = 34 // content plus its right padding
		leftPad  = 1
		rightPad = 1
		border   = 1
	)

	// Below this there is not enough room for two useful columns, so stack them
	// and let failures lead.
	if w < leftBox+40 {
		return strings.Join([]string{
			m.hookFailures(w), m.hookLatency(w), m.hookInjections(w),
			m.hookActivity(w), m.hookRecent(w),
		}, "\n")
	}

	rightBox := w - leftBox - border
	leftContent := m.hookActivity(leftBox - leftPad)
	rightContent := m.hookFailures(rightBox-rightPad) +
		"\n" + m.hookLatency(rightBox-rightPad) +
		"\n" + m.hookInjections(rightBox-rightPad)

	// Both columns are given the taller one's height. Otherwise the divider is
	// only as long as the activity list and stops mid-panel, which reads as a
	// rendering fault rather than as two columns.
	height := maxInt(lipgloss.Height(leftContent), lipgloss.Height(rightContent))

	left := lipgloss.NewStyle().
		Width(leftBox).
		Height(height).
		PaddingRight(leftPad).
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(faint).
		Render(leftContent)
	right := lipgloss.NewStyle().
		Width(rightBox).
		Height(height).
		PaddingLeft(rightPad).
		Render(rightContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, right) + "\n" + m.hookRecent(w)
}

// hookFailures is the reason this tab exists. Claude Code calls a non-zero hook
// exit "non-blocking" and carries on, so the only trace is a line in a
// transcript nobody reads.
func (m Model) hookFailures(w int) string {
	failing := groupFailures(m.hooks)
	if len(failing) == 0 {
		if len(m.hooks) == 0 {
			return dimStyle.Render("no hook records in this transcript") + "\n"
		}
		return goodStyle.Render("✓ no failing hooks") + "\n"
	}

	var b strings.Builder
	b.WriteString(badStyle.Render(fmt.Sprintf("✗ %d hook%s failing", len(failing), plural(len(failing)))) + "\n")
	nameW := maxInt(w-14, 12)
	for _, f := range failing {
		b.WriteString(badStyle.Render(pad(trunc(f.Name, nameW), nameW)) +
			dimStyle.Render(padLeft("exit "+fmt.Sprint(f.ExitCode), 9)) +
			faintStyle.Render(fmt.Sprintf(" ×%d", f.Count)) + "\n")
		if f.Command != "" {
			b.WriteString("  " + faintStyle.Render(trunc(f.Command, maxInt(w-2, 10))) + "\n")
		}
		if f.Stderr != "" {
			b.WriteString("  " + warnStyle.Render(trunc(cleanStderr(f.Stderr), maxInt(w-2, 10))) + "\n")
		}
	}
	if hint := failureHint(failing); hint != "" {
		b.WriteString("\n" + faintStyle.Render(wrap(hint, w)) + "\n")
	}
	return b.String()
}

// failureHint explains the failure mode when it is one with a known cause.
// Exit 127 is common enough, and its cause specific enough, to be worth naming.
func failureHint(failing []failure) string {
	for _, f := range failing {
		if f.ExitCode == 127 {
			return "exit 127 is PATH: hooks run under /bin/sh with no shell activation, " +
				"so an interpreter needs an absolute path."
		}
	}
	return ""
}

// hookLatency reports measured hook durations.
//
// Sourced from the transcript's own hook records, not from the duration_ms on a
// PostToolUse payload — that is how long the tool took, and reporting it as hook
// latency would be confidently wrong.
func (m Model) hookLatency(w int) string {
	timings := hookTimings(m.hooks)
	if len(timings) == 0 {
		return ""
	}
	nameW := maxInt(w-25, 14)

	var b strings.Builder
	b.WriteString("\n" + titleStyle.Render("Latency") + "\n")
	b.WriteString(faintStyle.Render(pad("hook", nameW)+padLeft("ran", 5)+padLeft("worst", 10)+padLeft("mean", 9)) + "\n")
	for _, t := range timings {
		style := dimStyle
		switch {
		case t.Worst > time.Second:
			style = badStyle
		case t.Worst > 200*time.Millisecond:
			style = warnStyle
		}
		b.WriteString(pad(truncPath(t.Name, nameW), nameW) +
			padLeft(numStyle.Render(fmt.Sprint(t.Count)), 5) +
			padLeft(style.Render(t.Worst.String()), 10) +
			padLeft(dimStyle.Render(t.Mean().String()), 9) + "\n")
	}
	b.WriteString(faintStyle.Render(wrap("a hook is on the critical path of the turn that triggered it.", w)))
	return b.String()
}

// hookActivity counts firings from the sidecar's own log, which is the only
// record of the hooks that ran without incident.
func (m Model) hookActivity(w int) string {
	rows := hookActivityRows(m.events, m.current.ID)

	var b strings.Builder
	b.WriteString(titleStyle.Render("Activity") + "\n")
	if len(rows) == 0 {
		b.WriteString(dimStyle.Render("nothing recorded yet") + "\n")
		return b.String()
	}
	nameW := maxInt(w-7, 12)
	b.WriteString(faintStyle.Render(pad("event", nameW)+padLeft("fired", 7)) + "\n")
	for _, r := range rows {
		b.WriteString(pad(trunc(r.name, nameW), nameW) +
			padLeft(numStyle.Render(fmt.Sprint(r.count)), 7) + "\n")
	}
	return b.String()
}

func (m Model) hookRecent(w int) string {
	recent := m.events
	if len(recent) > 10 {
		recent = recent[len(recent)-10:]
	}
	if len(recent) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n" + titleStyle.Render("Recent") + "\n")
	for i := len(recent) - 1; i >= 0; i-- {
		ev := recent[i]
		b.WriteString(dimStyle.Render(ev.TS.Local().Format("15:04:05")) + "  " +
			pad(ev.Event, 22) + faintStyle.Render(trunc(ev.Summary(), maxInt(w-34, 10))) + "\n")
	}
	return b.String()
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

// hookInjections reports hooks that add text to the context window.
//
// Hooks are allowed to inject context, and one that adds a page of instructions
// on every session start is a recurring cost that is otherwise attributed to
// nothing in particular.
func (m Model) hookInjections(w int) string {
	inj := injections(m.hooks)
	if len(inj) == 0 {
		return ""
	}
	nameW := maxInt(w-20, 12)

	var b strings.Builder
	b.WriteString("\n" + titleStyle.Render("Injecting context") + "\n")
	for _, f := range inj {
		b.WriteString(pad(trunc(f.Name, nameW), nameW) +
			padLeft(numStyle.Render(comma(f.Tokens)), 8) + dimStyle.Render(" tok") +
			faintStyle.Render(fmt.Sprintf(" ×%d", f.Count)) + "\n")
	}
	return b.String()
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

func hookActivityRows(events []event.Event, session string) []activityRow {
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

// pickerView is the session switcher.
//
// Each row carries the same bar as the header, so the choice is made on how full
// a session is and what filled it rather than on a uuid and a timestamp.
// Sessions are identified by directory, because that is how anyone thinks about
// them.
func (m Model) pickerView(termWidth int) string {
	// Wide enough for a readable bar, narrow enough to read as a dialog — but
	// never wider than the terminal, which a fixed minimum would allow.
	boxW := minInt(minInt(maxInt(termWidth-8, 30), 92), maxInt(termWidth-2, 20))
	contentW := boxW - panelBorder
	labelW, numW, barW, showTime := pickerColumns(contentW)

	names := sessionNames(m.sessions, m.summary, labelW-1)

	var b strings.Builder
	b.WriteString(titleStyle.Render("Switch session") + "\n\n")

	for i, s := range m.sessions {
		if i >= pickerSessions {
			b.WriteString(faintStyle.Render(fmt.Sprintf("  … %d more not shown",
				len(m.sessions)-pickerSessions)) + "\n")
			break
		}

		sum, known := m.summary[s.ID]

		marker, label := "  ", dimStyle.Render(pad(names[i], labelW))
		if i == m.pick {
			// A plain glyph, not a chip: chipOn pads to three columns and the
			// row is budgeted for two.
			marker = keyStyle.Render("▸") + " "
			label = titleStyle.Render(pad(names[i], labelW))
		}

		var figure, bar string
		switch {
		case !known:
			// Beyond the analysis budget, or an unreadable transcript. Say so
			// rather than drawing an empty bar that reads as an idle session.
			figure = faintStyle.Render(padLeft("—", numW))
			bar = faintStyle.Render(strings.Repeat("·", barW))
		default:
			window := sum.Window
			if window == 0 {
				window = defaultWindow
			}
			figure = thresholdStyle(sum.Total, window).Render(
				padLeft(fmt.Sprintf("%s / %s", compact(sum.Total), compact(window)), numW))
			bar = stackedGauge(sum.Slices, sum.Total, window, barW)
		}

		b.WriteString(marker + label + figure + " " + bar)
		if showTime {
			b.WriteString(" " + dimStyle.Render(s.Active.Local().Format("15:04:05")))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n" + helpStyle.Render("enter select · esc cancel · selecting pins the session"))
	return panel.Width(boxW).BorderForeground(accent).Render(b.String())
}

// elapsedLabel renders a duration only when one is actually known.
func elapsedLabel(a Agent) string {
	if d, ok := a.Elapsed(); ok {
		return d.String()
	}
	return "—"
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

// pickerColumns fits the session row to the space available, dropping the least
// useful columns rather than overflowing.
//
// Overflow here is not a cosmetic problem: the row wraps, every session takes
// two lines, and the timestamp of one lands under the label of the next. The
// list is already ordered by recency, so the clock is the first thing to go, and
// the label is squeezed before the bar because a truncated project name is still
// recognisable while a two-cell bar is not.
func pickerColumns(contentW int) (labelW, numW, barW int, showTime bool) {
	const (
		markerW  = 2
		fullNum  = 16 // "  335k / 1.0m"
		tightNum = 13
		timeW    = 9 // "15:04:05" plus its leading space
		fullLbl  = 34
		minLbl   = 14
		minBar   = 8
	)

	numW, showTime = fullNum, true
	// Each step gives up the least valuable thing still present.
	for _, step := range []func(){
		func() {},
		func() { numW = tightNum },
		func() { showTime = false },
	} {
		step()
		fixed := markerW + numW + 1 // the space before the bar
		if showTime {
			fixed += timeW
		}
		if labelW = minInt(fullLbl, contentW-fixed-minBar); labelW >= minLbl {
			return labelW, numW, contentW - fixed - labelW, showTime
		}
	}

	// Narrower than the columns can survive: keep a legible label and whatever
	// bar is left, however little that is.
	labelW = maxInt(contentW-markerW-numW-1, 8)
	return labelW, numW, maxInt(contentW-markerW-numW-1-labelW, 0), false
}
