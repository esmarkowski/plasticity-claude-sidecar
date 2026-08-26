package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/attrib"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/event"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/memory"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/transcript"
)

// rulesView answers "what instructions am I paying for, and why were they
// loaded". This is the tab the InstructionsLoaded hook exists for: none of this
// text appears in the transcript, so without the hook it is invisible.
func (m Model) rulesView(w int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Instruction files in context") + "\n\n")
	b.WriteString(m.bucketDetail(attrib.BucketRules, w, true))

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
	b.WriteString(m.memoryView(w))
	return b.String()
}

// memoryView lists the project's memory store.
//
// Under rules rather than in a tab of its own, because it is one short list and a
// tab would promise more than it can deliver. What it can say is what exists and
// what each would cost; what it cannot say is which were recalled, and that is
// said out loud rather than left to be assumed from a list of files.
func (m Model) memoryView(w int) string {
	store := m.memory
	if store.Empty() {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n" + titleStyle.Render("Memory") +
		dimStyle.Render("  — what this project has been told to remember") + "\n\n")

	nameW := maxInt(minInt(w-34, 46), 16)
	b.WriteString(faintStyle.Render(pad("", gutter)+pad("memory", nameW)+
		padLeft("tokens", 9)+padLeft("kind", 10)+"  in index") + "\n")

	// The index first and always, because it is the one part of this that is
	// measurably in the window.
	if store.Index.Tokens > 0 {
		b.WriteString(pad("", gutter) + pad(trunc(memory.IndexFile, nameW-1), nameW) +
			padLeft(numStyle.Render(comma(store.Index.Tokens)), 9) +
			padLeft(dimStyle.Render("index"), 10) +
			"  " + goodStyle.Render("loaded") + "\n")
	}
	for _, n := range store.Notes {
		mark := warnStyle.Render("orphan")
		if n.Indexed {
			mark = dimStyle.Render("yes")
		}
		b.WriteString(pad("", gutter) + pad(trunc(n.Title, nameW-1), nameW) +
			padLeft(dimStyle.Render(comma(n.Tokens)), 9) +
			padLeft(faintStyle.Render(n.Kind), 10) + "  " + mark + "\n")
	}

	b.WriteString("\n" + faintStyle.Render(wrap(fmt.Sprintf(
		"the index is loaded at session start; the %d memories under it are recalled on "+
			"demand and nothing records which, so these are what each would cost rather "+
			"than what is in the window now.", len(store.Notes)), w)) + "\n")
	if orphans := store.Orphans(); len(orphans) > 0 {
		b.WriteString(warnStyle.Render(fmt.Sprintf(
			"%d memor%s not linked from the index — on disk, and never recalled",
			len(orphans), map[bool]string{true: "y is", false: "ies are"}[len(orphans) == 1])) + "\n")
	}
	return b.String()
}

// toolsView ranks tools by what their results cost. Tool results are usually the
// largest single category, and they are the one category the user controls
// directly by choosing what to read.
func (m Model) toolsView(w int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Tool results") + dimStyle.Render("  — what came back into context") + "\n\n")
	b.WriteString(m.bucketDetail(attrib.BucketToolResults, w, false))
	b.WriteString("\n\n" + titleStyle.Render("Tool calls") + dimStyle.Render("  — what we sent") + "\n\n")
	b.WriteString(m.bucketDetail(attrib.BucketToolCalls, w, false))
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
	fixed := gutter + markerW + typeW + stateW + ctxW + reqW + elapsedW + replyW + 1
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
	header := pad("", gutter+markerW) + pad("agent", typeW)
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
			// Only a running agent reaches here: its transcript is not written
			// yet. Agents whose transcript is genuinely absent are not listed
			// at all, and are reported as a count below.
			bar = faintStyle.Render(strings.Repeat("·", barW))
		}

		ref := rowRef{kindAgent, a.ID}
		on := m.on(kindAgent, a.ID)
		mark := strings.Repeat(" ", markerW)
		if m.hasBreakdown(ref) {
			mark = foldMark(m.expanded[ref]) + " "
		}
		row := m.rowGutter(on) + mark +
			pad(selectedName(trunc(a.Label(), typeW-1), on), typeW)
		if taskW > 0 {
			row += pad(dimStyle.Render(trunc(a.Task, taskW-1)), taskW)
		}
		row += pad(state, stateW) + padLeft(ctx, ctxW) + padLeft(reqs, reqW) +
			padLeft(dimStyle.Render(elapsedLabel(a)), elapsedW) + padLeft(reply, replyW)
		b.WriteString(row + " " + bar + "\n")
		if m.open(ref) {
			b.WriteString(agentDetail(a, taskW, agentCols{
				name: gutter + markerW + typeW + taskW + stateW,
				num:  ctxW,
				rest: reqW + elapsedW + replyW,
				bar:  barW,
			}))
		}
	}

	b.WriteString("\n" + faintStyle.Render(wrap(
		"bar length is how full that agent's own window is; colours match the context legend.", w)))
	if m.untracedAgents > 0 {
		// Said once rather than listed. A row for one of these would carry eight
		// characters of an id and nothing else, which reads as an agent that ran
		// and returned nothing — a claim about the agent, when what is missing is
		// the file.
		b.WriteString("\n" + faintStyle.Render(wrap(fmt.Sprintf(
			"%d agent%s ran with no transcript at the path SubagentStop named, so neither "+
				"type, task, nor context could be read. Not listed above.",
			m.untracedAgents, plural(m.untracedAgents)), w)))
	}
	return b.String()
}

// agentCols mirrors the agent row's columns, so a nested figure lands under the
// one it is part of rather than somewhere to the right of it.
type agentCols struct{ name, num, rest, bar int }

// agentDetail names the segments of an agent's bar.
//
// The bar was already the agent's own context composition — that is what
// stackedGauge draws — but unlabelled, so it showed that a subagent had filled
// its window without saying on what. The swatches are the category colours
// rather than shades of one hue, because these are the categories, and they mean
// the same thing here as on the context tab.
func agentDetail(a Agent, taskW int, cols agentCols) string {
	var b strings.Builder
	for _, s := range a.Report.Slices {
		swatch := lipgloss.NewStyle().Foreground(colorFor(s.Bucket)).Render("▊")
		bar := lipgloss.NewStyle().Foreground(fade(colorFor(s.Bucket), nestedFade))
		// Shares and bars are of the agent's own total, not of the window. The bar
		// above already answers how full this agent got; these answer what filled
		// it, and against the window every category would round to one cell.
		b.WriteString(nested(swatch, string(s.Bucket), cols.name) +
			padLeft(dimStyle.Render(comma(s.Tokens)), cols.num) +
			padLeft(faintStyle.Render(sharePct(s.Tokens, a.Report.Total)), cols.rest) + " " +
			bar.Render(strings.Repeat("▊", barCells(s.Tokens, a.Report.Total, cols.bar))) + "\n")
	}
	// Only when the column actually cut it off, which is where the interesting
	// half of a prompt usually starts. Repeating a task that already fits would
	// just be the same line twice.
	if lipgloss.Width(a.Task) > taskW-1 {
		b.WriteString(indent(faintStyle.Render(a.Task),
			gutter+markerW+2, cols.name+cols.num+cols.rest+cols.bar))
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
//
// A transcript is a whole session's history, so a hook fixed mid-session keeps
// its failures on the record forever. Two things retire them: a clean run of
// the same hook, which is proof the fix took, and `x`, for the hook that was
// removed from settings.json and will never run again to prove anything.
func (m Model) hookFailures(w int) string {
	current, resolved, dismissed := m.failureGroups()

	var b strings.Builder
	switch {
	case len(current) > 0:
		b.WriteString(headline(
			badStyle.Render(fmt.Sprintf("✗ %d hook%s failing", len(current), plural(len(current)))),
			faintStyle.Render("x to dismiss"), w) + "\n")
	case len(m.hooks) == 0:
		b.WriteString(dimStyle.Render("no hook records in this transcript") + "\n")
	default:
		b.WriteString(goodStyle.Render("✓ no failing hooks") + "\n")
	}

	nameW := maxInt(w-14-gutter, 12)
	for _, f := range current {
		on := m.on(kindHook, f.key())
		style := badStyle
		if on {
			style = accentStyle
		}
		b.WriteString(m.rowGutter(on) + style.Render(pad(trunc(f.Name, nameW), nameW)) +
			dimStyle.Render(padLeft("exit "+fmt.Sprint(f.ExitCode), 9)) +
			faintStyle.Render(fmt.Sprintf(" ×%d", f.Count)) + "\n")
		if f.Command != "" {
			b.WriteString(pad("", gutter+2) +
				faintStyle.Render(trunc(f.Command, maxInt(w-2-gutter, 10))) + "\n")
		}
		if f.Stderr != "" {
			b.WriteString(pad("", gutter+2) +
				warnStyle.Render(trunc(cleanStderr(f.Stderr), maxInt(w-2-gutter, 10))) + "\n")
		}
	}
	if hint := failureHint(current); hint != "" {
		b.WriteString("\n" + faintStyle.Render(wrap(hint, w)) + "\n")
	}
	b.WriteString(m.hookResolved(resolved, dismissed, w))
	return b.String()
}

// hookResolved lists the failures a later clean run already answered.
//
// Listed rather than dropped, because the two are not the same story: a hook
// that failed forty times and now passes says the fix landed, and a hook that
// never failed says nothing was ever wrong. The count is the evidence.
func (m Model) hookResolved(resolved []failure, dismissed, w int) string {
	if len(resolved) == 0 && dismissed == 0 {
		return ""
	}
	var b strings.Builder
	if len(resolved) > 0 {
		b.WriteString("\n" + titleStyle.Render("Resolved") + "\n")
		nameW := maxInt(w-23-gutter, 12)
		for _, f := range resolved {
			on := m.on(kindHook, f.key())
			// Styled before padding: an inner reset would swallow the selection
			// colour if the label were wrapped after dimStyle had run.
			style := dimStyle
			if on {
				style = accentStyle
			}
			b.WriteString(m.rowGutter(on) +
				style.Render(pad(trunc(f.Name, nameW), nameW)) +
				faintStyle.Render(padLeft("exit "+fmt.Sprint(f.ExitCode), 9)) +
				faintStyle.Render(padLeft(fmt.Sprintf("×%d", f.Count), 5)) +
				goodStyle.Render(padLeft(passingSince(f.Since), 9)) + "\n")
		}
	}
	if dismissed > 0 {
		b.WriteString("\n" + faintStyle.Render(
			fmt.Sprintf("%d dismissed · X to restore", dismissed)) + "\n")
	}
	return b.String()
}

// passingSince names when the fix landed. Time only: a transcript is one
// session, and a date would be the same on every row.
func passingSince(t time.Time) string {
	if t.IsZero() {
		return "✓ passing"
	}
	return "✓ " + t.Local().Format("15:04")
}

// headline puts a hint hard right of a heading, and drops it when the column is
// too narrow to hold both — a wrapped heading costs more than the hint is worth.
func headline(left, right string, w int) string {
	if lipgloss.Width(left)+lipgloss.Width(right)+2 > w {
		return left
	}
	return pad(left, w-lipgloss.Width(right)) + right
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
	// Last is the most recent failing run, and what a dismissal is measured
	// against: a failure after the dismissal is a new failure and comes back.
	Last time.Time
	// Resolved records that a later run of the same hook came back clean. A
	// hook proves it is fixed by running, so its failures stop being current
	// the moment one does.
	Resolved bool
	// Since is when the first clean run after the last failure happened.
	Since time.Time
}

// key identifies a failure across refreshes, which is what a dismissal has to
// outlive. Exit code is part of it because a hook that fails a second way is a
// second problem, and dismissing the first should not hide it.
func (f failure) key() string { return f.Name + "#" + fmt.Sprint(f.ExitCode) }

// groupFailures collapses failing runs into one row per hook and exit code, and
// marks the ones a later clean run has already answered.
//
// Position in the transcript decides what came later, not the timestamp: order
// is the one thing every record has, and the stop-summary entries carry no hook
// name to compare against anyway.
func groupFailures(hooks []transcript.HookRun) []failure {
	byKey := map[string]*failure{}
	lastAt := map[string]int{}
	var order []string
	for i, h := range hooks {
		if !h.Failed {
			continue
		}
		f := &failure{Name: h.Name, Command: h.Command, Stderr: h.Stderr, ExitCode: h.ExitCode}
		key := f.key()
		if prev, ok := byKey[key]; ok {
			f = prev
		} else {
			byKey[key] = f
			order = append(order, key)
		}
		f.Count++
		if h.TS.After(f.Last) {
			f.Last = h.TS
		}
		lastAt[key] = i
	}

	for key, f := range byKey {
		f.Resolved, f.Since = resolvedBy(hooks[lastAt[key]+1:], f.Name)
	}

	out := make([]failure, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	sort.Slice(out, func(i, j int) bool {
		// Still-broken first: that is the only part of this list that asks for
		// anything to be done.
		if out[i].Resolved != out[j].Resolved {
			return !out[i].Resolved
		}
		return out[i].Count > out[j].Count
	})
	return out
}

// resolvedBy reports whether a named hook ran cleanly in the runs that followed
// its last failure, and when.
//
// Anonymous runs are ignored. The stop-summary entries record a command and a
// duration but no hook name, so treating them as clean runs would clear a
// failure on the strength of some other hook succeeding.
func resolvedBy(later []transcript.HookRun, name string) (bool, time.Time) {
	if name == "" {
		return false, time.Time{}
	}
	for _, h := range later {
		if !h.Failed && h.Name == name {
			return true, h.TS
		}
	}
	return false, time.Time{}
}

// failureGroups splits the transcript's failures into what is still current,
// what a clean run has since resolved, and how many the user has dismissed.
//
// A dismissal is a watermark, not a delete: it hides the failure as it stood
// when dismissed, so the same hook failing again afterwards surfaces on its own
// rather than staying silently hidden.
func (m Model) failureGroups() (current, resolved []failure, dismissed int) {
	for _, f := range groupFailures(m.hooks) {
		if at, ok := m.dismissed[f.key()]; ok && !f.Last.After(at) {
			dismissed++
			continue
		}
		if f.Resolved {
			resolved = append(resolved, f)
			continue
		}
		current = append(current, f)
	}
	return current, resolved, dismissed
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
	const (
		reqW      = 6
		ctxW      = 11
		deltaW    = 9
		measuredW = 11
		residualW = 11
		thinkingW = 10
		minCause  = 12
	)
	// The cause is the widest thing here and the first to go when the panel is
	// narrow; the numbers are the point of the tab and keep their room.
	fixed := gutter + markerW + reqW + ctxW + deltaW + measuredW + residualW + thinkingW
	causeW := 0
	if w-fixed >= minCause {
		causeW = minInt(w-fixed, 30)
	}

	header := pad("", gutter+markerW) + pad("req", reqW)
	if causeW > 0 {
		header += pad("grew by", causeW)
	}
	header += padLeft("context", ctxW) + padLeft("Δ", deltaW) +
		padLeft("measured", measuredW) + padLeft("residual", residualW) +
		padLeft("thinking", thinkingW)
	b.WriteString(faintStyle.Render(header) + "\n")

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
		ref := rowRef{kindRequest, fmt.Sprint(p.Request)}
		on := m.on(kindRequest, ref.Name)
		mark := strings.Repeat(" ", markerW)
		if m.hasBreakdown(ref) {
			mark = foldMark(m.expanded[ref]) + " "
		}
		row := m.rowGutter(on) + mark + pad(selectedName(ref.Name, on), reqW)
		if causeW > 0 {
			row += pad(dimStyle.Render(trunc(cause(p), causeW-1)), causeW)
		}
		row += padLeft(numStyle.Render(comma(p.Context)), ctxW) +
			padLeft(delta, deltaW) +
			padLeft(dimStyle.Render(comma(p.Measured)), measuredW) +
			padLeft(dimStyle.Render(comma(p.Residual)), residualW) +
			padLeft(dimStyle.Render(comma(p.Thinking)), thinkingW)
		b.WriteString(row + "\n")
		if m.open(ref) {
			b.WriteString(requestDetail(p, gutter+markerW+reqW+causeW+ctxW, deltaW, measuredW))
		}
		prev = p.Context
	}
	b.WriteString("\n" + faintStyle.Render(
		"residual is context minus everything measurable. If it climbs steadily, a category is being missed."))
	return b.String()
}

// cause is the largest thing that can be named in a request's growth, for the
// column that saves expanding every row to find it.
//
// "unexplained" is skipped even where it leads. It is the honest largest entry
// and the residual column already reports it, but as a row label it says nothing
// — the question the column answers is which command did this.
func cause(p attrib.AuditPoint) string {
	for _, it := range p.Detail {
		switch it.Name {
		case "unexplained", "other":
		default:
			return it.Name
		}
	}
	return ""
}

// requestDetail names what landed in the window before this request.
//
// The columns can say the context grew by eight thousand tokens; only this can
// say which Read did it. Shares are of what could be named rather than of the
// jump, because the jump is exact and these are estimates — sharing them out of
// the jump would report more than a hundred percent as often as not.
func requestDetail(p attrib.AuditPoint, nameW, numW, shareW int) string {
	total := 0
	for _, it := range p.Detail {
		total += it.Tokens
	}
	var b strings.Builder
	for _, it := range p.Detail {
		b.WriteString(nested("", it.Name, nameW) +
			padLeft(dimStyle.Render(comma(it.Tokens)), numW) +
			padLeft(faintStyle.Render(sharePct(it.Tokens, total)), shareW) + "\n")
	}
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
