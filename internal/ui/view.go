package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/attrib"
)

// A panel costs 2 columns of border plus 2 of padding. lipgloss sizes a style
// by its content box including padding but excluding border, so a panel that
// should span W columns is given Width(W-2) and has W-4 columns for text.
const (
	panelBorder = 2
	panelChrome = 4
)

// defaultWindow is assumed only until a harness probe reports the real limit.
const defaultWindow = 1_000_000

// panelWidth converts a text width into the Width() a panel needs to end up
// exactly panelChrome wider.
func panelWidth(text int) int { return text + panelChrome - panelBorder }

func (m Model) View() string {
	if m.width == 0 {
		return "starting…"
	}
	if m.picker {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			m.pickerView(m.width), lipgloss.WithWhitespaceChars(" "))
	}
	// The body comes out of the viewport, which was filled and scrolled in
	// refresh: content and geometry have to be settled before a mouse wheel can
	// be clamped against them, and Update is the only place that can happen.
	inner := m.width - panelChrome
	return strings.Join([]string{
		m.header(inner),
		m.bar.row,
		panel.Width(panelWidth(inner)).Render(m.vp.View()),
		m.footer(inner),
	}, "\n")
}

// refresh re-lays out the whole frame: chip row, viewport size, viewport
// content, and the scroll offset needed to keep the cursor in view.
//
// Done in Update rather than in View because the viewport has to be holding the
// current content and size before the next message arrives — a mouse wheel is
// clamped against how many lines there are, and a stale viewport clamps against
// the wrong number.
func (m *Model) refresh() {
	if m.width == 0 || m.height == 0 {
		return
	}
	inner := m.width - panelChrome

	m.bar = m.tabsBar(inner)
	header := m.header(inner)
	// Measure the chrome rather than assuming it. The header grows a row when
	// hooks are failing, and a hardcoded height would silently eat the last line
	// of every tab whenever it did.
	m.bar.y = lipgloss.Height(header)
	// Where the body's first content line lands on screen: the chip row, then the
	// panel's top border. Recorded here because this is where the geometry is
	// known, and a click arrives with nothing but coordinates.
	m.bodyTop = m.bar.y + 2
	chrome := lipgloss.Height(header) + lipgloss.Height(m.bar.row) +
		lipgloss.Height(m.footer(inner)) + panelBorder

	m.vp.Width = inner
	m.vp.Height = maxInt(m.height-chrome, 3)

	body := m.body(inner)
	m.vp.SetContent(body)
	if m.chase {
		m.scrollToCursor(strings.Split(body, "\n"))
		m.chase = false
	}
}

// scrollToCursor brings the cursor's row into view, and leaves the offset alone
// when it is already there.
//
// Only called when the cursor has just moved. Running it on every refresh meant
// the two-second reload dragged the view back to wherever the cursor was parked,
// so scrolling the timeline by wheel lasted about two seconds.
//
// The cursor is found by looking for its marker rather than tracked as a line
// number, because a row is not one line — a tool with its breakdown open is
// seven — and every renderer would otherwise have to report where it put things.
func (m *Model) scrollToCursor(lines []string) {
	at := markedLine(lines)
	if at < 0 {
		return
	}
	switch {
	case at < m.vp.YOffset:
		m.vp.SetYOffset(at)
	case at >= m.vp.YOffset+m.vp.Height:
		m.vp.SetYOffset(at - m.vp.Height + 1)
	}
}

// header is always visible: which session, how full, and under what settings.
// Everything here answers "am I looking at the thing I think I am".
func (m Model) header(w int) string {
	r := m.report

	name := m.current.Label()
	if r.Title != "" {
		name = r.Title
	}
	if m.pinned {
		name += " " + warnStyle.Render("pinned")
	}

	window := r.Window
	if window == 0 {
		window = defaultWindow
	}
	pct := 0.0
	if window > 0 {
		pct = float64(r.Total) / float64(window) * 100
	}

	left := titleStyle.Render(name)
	right := dimStyle.Render(strings.Join(nonEmpty(
		r.Model, r.Branch, m.report.PermMode, m.report.Effort,
	), " · "))
	if lipgloss.Width(left)+lipgloss.Width(right) > w {
		left = titleStyle.Render(trunc(name, maxInt(w-lipgloss.Width(right)-1, 8)))
	}
	line1 := pad(left, w-lipgloss.Width(right)) + right

	tokens := fmt.Sprintf("%s / %s", comma(r.Total), compact(window))
	stat := thresholdStyle(r.Total, window).Bold(true).Render(tokens) + "  " +
		dimStyle.Render(fmt.Sprintf("%.1f%%", pct)) + "  " +
		dimStyle.Render(fmt.Sprintf("%d requests", r.Turns))

	// The bar takes whatever the stats leave. Categories below roughly one
	// column's worth round away at this width — the legend's own bars are scaled
	// to the largest category rather than to the window, so they stay visible
	// there.
	line2 := stat + "  " + stackedGauge(r.Slices, r.Total, window, maxInt(w-lipgloss.Width(stat)-2, 8))

	return panel.Width(panelWidth(w)).Render(
		strings.Join([]string{line1, line2, dimStyle.Render(truncPath(r.CWD, w))}, "\n"))
}

func (m Model) footer(w int) string {
	live := goodStyle.Render("● live")
	switch {
	case m.err != nil:
		live = badStyle.Render("● " + trunc(m.err.Error(), 40))
	case !m.report.Probed:
		live = warnStyle.Render("● estimated · sidecar probe")
	}

	// Where the body is scrolled to. The viewport draws no banner of its own, and
	// being scrolled has to be visible: without this the view is indistinguishable
	// from a short tab, and content above the fold — which is where anything
	// urgent is put — silently does not exist.
	if !(m.vp.AtTop() && m.vp.AtBottom()) {
		live = dimStyle.Render(fmt.Sprintf("↕ %.0f%%  ", m.vp.ScrollPercent()*100)) + live
	}

	// Drop hints from the right until the row fits. A footer that wraps pushes
	// the whole layout up by a line and makes the dashboard jitter as the
	// status text changes length.
	//
	// j/k is described by what it does on this tab, since on a tab with rows it
	// moves the cursor and on one without it scrolls.
	move, fold := "j/k scroll", ""
	if len(m.selectableRows()) > 0 {
		move, fold = "j/k select", "←/→ fold"
	}
	full := nonEmpty("tab tabs", move, fold, m.enterHint(),
		"s session", "p pin", "r refresh", "q quit")
	for n := len(full); n > 0; n-- {
		left := helpStyle.Render(strings.Join(full[:n], "  ·  "))
		if lipgloss.Width(left)+lipgloss.Width(live)+1 <= w {
			return pad(left, w-lipgloss.Width(live)) + live
		}
	}
	return padLeft(live, w)
}

// enterHint names what enter does to the selected row, which is different on
// every tab. A key whose effect is not named is a key nobody presses — and one
// that does nothing to this row should not be advertised at all.
func (m Model) enterHint() string {
	ref, ok := m.selected()
	switch {
	case !ok:
		return ""
	case ref.Kind == kindBucket:
		if _, ok := detailTab[attrib.Bucket(ref.Name)]; !ok {
			return ""
		}
		return "enter detail"
	case ref.Kind == kindHook:
		return "enter dismiss"
	case m.hasBreakdown(ref):
		if m.expanded[ref] {
			return "enter collapse"
		}
		return "enter expand"
	}
	return ""
}

func (m Model) body(w int) string {
	if m.err != nil && m.report.Total == 0 {
		return m.emptyView()
	}
	switch m.tab {
	case TabContext:
		return m.contextView(w)
	case TabRules:
		return m.rulesView(w)
	case TabTools:
		return m.toolsView(w)
	case TabAgents:
		return m.agentsView(w)
	case TabHooks:
		return m.hooksView(w)
	case TabTimeline:
		return m.timelineView(w)
	}
	return ""
}

func (m Model) emptyView() string {
	return strings.Join([]string{
		titleStyle.Render("Nothing to show yet."),
		"",
		dimStyle.Render("The dashboard reads two things:"),
		"  " + keyStyle.Render("events") + dimStyle.Render("      ~/.claude/sidecar/events.jsonl, written by the hooks"),
		"  " + keyStyle.Render("transcripts") + dimStyle.Render(" ~/.claude/projects/*/*.jsonl, written by Claude Code"),
		"",
		dimStyle.Render("If this is a fresh install, run ") + keyStyle.Render("sidecar install") +
			dimStyle.Render(" and start a session."),
	}, "\n")
}

// contextView is the headline: one stacked bar, then a legend ordered by size.
func (m Model) contextView(w int) string {
	r := m.report
	if len(r.Slices) == 0 {
		return dimStyle.Render("no messages in context yet")
	}

	var b strings.Builder
	largest := r.Slices[0].Tokens
	const (
		swatchW = 2 // swatch plus its trailing space
		nameW   = 24
		numW    = 10
		pctW    = 7
		gapW    = 1
	)
	barW := maxInt(w-gutter-swatchW-nameW-numW-pctW-gapW, 6)

	for _, s := range r.Slices {
		pct := 0.0
		if r.Total > 0 {
			pct = float64(s.Tokens) / float64(r.Total) * 100
		}
		on := m.on(kindBucket, string(s.Bucket))
		swatch := lipgloss.NewStyle().Foreground(colorFor(s.Bucket)).Render("▐")
		// A category with a tab of its own says so, since enter goes there.
		name := string(s.Bucket)
		if _, ok := detailTab[s.Bucket]; ok && on {
			name += " →"
		}
		fmt.Fprintf(&b, "%s%s %s%s%s %s\n",
			m.rowGutter(on),
			swatch,
			pad(selectedName(trunc(name, nameW-1), on), nameW),
			padLeft(numStyle.Render(comma(s.Tokens)), numW),
			padLeft(dimStyle.Render(fmt.Sprintf("%.1f%%", pct)), pctW),
			miniBar(s.Tokens, largest, barW, s.Bucket),
		)
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf(
		"cache  %s read · %s written · %s uncached",
		comma(r.Usage.CacheReadInputTokens),
		comma(r.Usage.CacheCreationInputTokens),
		comma(r.Usage.InputTokens))))
	if r.Compacted {
		b.WriteString("\n" + warnStyle.Render(fmt.Sprintf(
			"compacted — the window had reached %s before collapsing", comma(r.PreTokens))))
	}
	if r.Probed {
		b.WriteString("\n" + dimStyle.Render("static half measured by /context at "+r.ProbedAt))
	} else {
		b.WriteString("\n" + warnStyle.Render(
			"system prompt and tool schemas inferred by subtraction — run `sidecar probe` to measure them"))
	}
	return b.String()
}

// rowRef names one selectable row: which list it belongs to, and which row.
//
// A name rather than an index, because a refresh reorders the rows underneath
// the cursor — most of them are sorted by size — and an index would quietly move
// the selection to whatever happened to grow.
type rowRef struct {
	Kind string
	Name string
}

// Row kinds that are not a bucket's drill-down. Buckets use their own name, so
// the tools tab's two tables stay distinct without a second field.
const (
	kindBucket  = "bucket"
	kindAgent   = "agent"
	kindHook    = "hook"
	kindRequest = "request"
)

// selectableRows is what the cursor can land on in the current tab, in the order
// they are drawn. Empty for a tab with nothing to select, which is what makes
// j/k fall back to scrolling there.
func (m Model) selectableRows() []rowRef {
	var out []rowRef
	switch m.tab {
	case TabContext:
		for _, s := range m.report.Slices {
			out = append(out, rowRef{kindBucket, string(s.Bucket)})
		}
	case TabRules:
		out = append(out, bucketRows(m.report, attrib.BucketRules)...)
	case TabTools:
		out = append(out, bucketRows(m.report, attrib.BucketToolResults)...)
		out = append(out, bucketRows(m.report, attrib.BucketToolCalls)...)
	case TabAgents:
		for _, a := range m.agents {
			out = append(out, rowRef{kindAgent, a.ID})
		}
	case TabHooks:
		current, resolved, _ := m.failureGroups()
		for _, f := range append(current, resolved...) {
			out = append(out, rowRef{kindHook, f.key()})
		}
	case TabTimeline:
		for _, p := range m.audit {
			out = append(out, rowRef{kindRequest, fmt.Sprint(p.Request)})
		}
	}
	return out
}

func bucketRows(r attrib.Report, b attrib.Bucket) []rowRef {
	var out []rowRef
	for _, it := range itemsOf(r, b) {
		out = append(out, rowRef{string(b), it.Name})
	}
	return out
}

// selected is the row the cursor is on, clamped: the list shrinks when a session
// is switched or a hook stops failing, and a stale cursor should land on the last
// row rather than nowhere.
func (m Model) selected() (rowRef, bool) {
	rows := m.selectableRows()
	if len(rows) == 0 {
		return rowRef{}, false
	}
	return rows[minInt(maxInt(m.cursor[m.tab], 0), len(rows)-1)], true
}

// on reports whether the cursor is on a row. Every renderer that draws rows
// calls this, which is what makes the cursor mean the same thing everywhere.
func (m Model) on(kind, name string) bool {
	here, ok := m.selected()
	return ok && here == rowRef{kind, name}
}

// itemsOf finds a bucket's rows in the report.
func itemsOf(r attrib.Report, b attrib.Bucket) []attrib.Item {
	for i := range r.Slices {
		if r.Slices[i].Bucket == b {
			return r.Slices[i].Detail
		}
	}
	return nil
}

// cursorMark is drawn in the gutter of the selected row, and is also how the
// viewport finds that row: the alternative is threading a line number back out
// through every renderer that might contain one.
const cursorMark = "›"

// gutter is the cursor's column. Two cells on every row of every selectable
// list, so turning the cursor on does not shift the numbers sideways.
const gutter = 2

// rowGutter is the cursor's column for one row: its marker, or the blank that
// keeps every other row in the same columns.
//
// Under a geometry probe every row gets the marker, which is what turns a single
// render into the whole line map.
func (m Model) rowGutter(on bool) string {
	if on || m.probing {
		return accentStyle.Render(cursorMark) + " "
	}
	return strings.Repeat(" ", gutter)
}

// nested indents a row under its parent, with a swatch when there is a bar above
// for it to point at.
func nested(swatch, name string, w int) string {
	lead := strings.Repeat(" ", gutter) + "  "
	if swatch != "" {
		lead += swatch + " "
	}
	return pad(lead+trunc(name, maxInt(w-lipgloss.Width(lead), 4)), w)
}

// selectedName styles a row's label so the selection is legible without a
// background: an inner style resets the background, so a highlighted row would
// come out striped wherever the row has colour of its own.
func selectedName(s string, on bool) string {
	if on {
		return accentStyle.Render(s)
	}
	return s
}

// detailCols is the column layout of a drill-down table. Sized once from the
// panel width and shared by the top-level rows and the rows nested under them,
// which is the only way the two sets of numbers line up.
type detailCols struct {
	name, num, share, count, note, bar int
}

// childIndent is the width a nested row's swatch and indent take out of the name
// column: two spaces, a block, and a space.
const childIndent = 4

// markerW is the fold marker's column, held open on every row so that labels all
// start in the same place whether or not they have anything to fold.
const markerW = 2

// layout sizes the columns for a set of rows.
//
// The name column is sized to the widest top-level name and never to the nested
// ones. Sizing it to everything meant the column grew the first time a long
// command appeared under Bash, and every number in the table stepped sideways
// mid-refresh — the nested names are truncated to fit instead.
func layout(items []attrib.Item, w int) detailCols {
	c := detailCols{num: 10, share: 7, count: 6}
	const gap = 1

	c.name = 12 + gutter + markerW
	for _, it := range items {
		if n := lipgloss.Width(it.Name) + 1 + gutter + markerW; n > c.name {
			c.name = n
		}
	}

	// A note column only when there is something to put in it. The rules tab uses
	// it for why a file was loaded, which is the whole reason that tab exists; the
	// tools tab has nothing to say there.
	for _, it := range items {
		if it.Note == "" {
			continue
		}
		if n := lipgloss.Width(it.Note) + 2; n > c.note {
			c.note = n
		}
	}
	c.note = minInt(c.note, 22)

	c.name = minInt(c.name, maxInt(w-c.num-c.share-c.count-c.note-gap-10, 14+gutter+markerW))
	c.bar = maxInt(w-c.name-c.num-c.share-c.count-c.note-gap, 6)
	return c
}

// bucketDetail renders the drill-down rows behind one bucket: what, how much,
// and what share of the category.
//
// Bars are scaled to the largest row rather than to the window, which is what
// the share column is for. Scaled to the window every row here would be a
// sliver, since a single category is a fraction of the context to begin with.
func (m Model) bucketDetail(bucket attrib.Bucket, w int, pathStyle bool) string {
	var slice *attrib.Slice
	for i := range m.report.Slices {
		if m.report.Slices[i].Bucket == bucket {
			slice = &m.report.Slices[i]
			break
		}
	}
	if slice == nil || len(slice.Detail) == 0 {
		return dimStyle.Render("nothing in this category yet")
	}
	cols := layout(slice.Detail, w)
	here, hasCursor := m.selected()

	var b strings.Builder
	header := pad("", cols.name) + padLeft("tokens", cols.num) +
		padLeft("share", cols.share) + padLeft("uses", cols.count)
	if cols.note > 0 {
		header += pad("  loaded", cols.note)
	}
	b.WriteString(faintStyle.Render(header) + "\n")

	largest := slice.Detail[0].Tokens
	for _, it := range slice.Detail {
		ref := rowRef{string(bucket), it.Name}
		on := hasCursor && here == ref
		open := len(it.Children) > 0 && m.expanded[ref]

		// The fold marker's column is held open on every row, blank where there
		// is nothing to fold. Drawing it only where it applies left the labels
		// starting in two different places down one list.
		mark := strings.Repeat(" ", markerW)
		if len(it.Children) > 0 {
			mark = foldMark(open) + " "
		}
		room := cols.name - 1 - gutter - markerW
		label := trunc(it.Name, room)
		if pathStyle {
			label = truncPath(it.Name, room)
		}
		name := m.rowGutter(on) + mark + selectedName(label, on)

		row := pad(name, cols.name) +
			padLeft(numStyle.Render(comma(it.Tokens)), cols.num) +
			padLeft(dimStyle.Render(sharePct(it.Tokens, slice.Tokens)), cols.share) +
			padLeft(uses(it.Count), cols.count)
		if cols.note > 0 {
			row += pad("  "+dimStyle.Render(trunc(it.Note, cols.note-2)), cols.note)
		}
		bar := miniBar(it.Tokens, largest, cols.bar, bucket)
		if open {
			bar = segmentBar(it.Children, it.Tokens, largest, cols.bar, bucket)
		}
		b.WriteString(row + " " + bar + "\n")
		if open {
			b.WriteString(childDetail(it, largest, cols, bucket))
		}
	}

	b.WriteString("\n" + faintStyle.Render(fmt.Sprintf("%d entries · %s tokens · %s of context",
		len(slice.Detail), comma(slice.Tokens), sharePct(slice.Tokens, m.report.Total))))
	return b.String()
}

// foldMark shows whether a row's breakdown is open, in the same glyphs a file
// tree uses, because that is what it is.
func foldMark(open bool) string {
	if open {
		return dimStyle.Render("▾")
	}
	return dimStyle.Render("▸")
}

// childDetail lists the parts one row is made of, under it.
//
// Each row's swatch is the shade its segment has in the parent's bar, and its
// own bar is drawn at the table's scale, so it is exactly as long as that
// segment. That is what makes the bar above readable: without the swatches it is
// a gradient, and with them it is a legend.
func childDetail(it attrib.Item, largest int, cols detailCols, bucket attrib.Bucket) string {
	if len(it.Children) == 0 {
		return ""
	}
	ramp := shades(bucket, len(it.Children))

	var b strings.Builder
	for i, kid := range it.Children {
		// The swatch keeps its segment's exact shade, since that is what ties the
		// row to the bar above; only the bar fades.
		swatch := lipgloss.NewStyle().Foreground(ramp[i]).Render("▊")
		name := strings.Repeat(" ", gutter) + "  " + swatch + " " +
			trunc(kid.Name, maxInt(cols.name-childIndent-gutter, 4))
		row := pad(name, cols.name) +
			padLeft(dimStyle.Render(comma(kid.Tokens)), cols.num) +
			padLeft(faintStyle.Render(sharePct(kid.Tokens, it.Tokens)), cols.share) +
			padLeft(uses(kid.Count), cols.count)
		if cols.note > 0 {
			row += pad("", cols.note)
		}
		b.WriteString(row + " " + lipgloss.NewStyle().Foreground(fade(ramp[i], nestedFade)).
			Render(strings.Repeat("▊", barCells(kid.Tokens, largest, cols.bar))) + "\n")
	}
	return b.String()
}

// uses renders a call count, and nothing for the single use that a count would
// only add noise to.
func uses(n int) string {
	if n <= 1 {
		return dimStyle.Render("—")
	}
	return warnStyle.Render(fmt.Sprintf("×%d", n))
}

// rowAt resolves a body line to the row that drew it, and to the last row above
// it when the line belongs to a breakdown rather than to a row of its own.
func (m Model) rowAt(line int) (int, bool) {
	found := -1
	for i, at := range m.rowLines() {
		if at > line {
			break
		}
		found = i
	}
	// Above the first row is the table header, which owns nothing.
	if found < 0 {
		return 0, false
	}
	return found, true
}

// rowLines is the body line each row's marker sits on, in the order they were
// drawn.
//
// Taken from a render with every row marked, rather than from geometry each
// renderer reports for itself. A row is not one line, and the tabs lay themselves
// out differently enough that the hooks tab draws its rows in a column beside
// another one — so the only account of where a row ended up that cannot drift
// from what is on screen is the screen itself.
//
// One render, not one per candidate: a binary search over renders was correct and
// cost a quarter of a second per click on a seven-hundred-request timeline, which
// is more than a click can afford.
func (m Model) rowLines() []int {
	probe := m
	probe.probing = true
	var out []int
	for i, l := range strings.Split(probe.body(m.vp.Width), "\n") {
		if marked(l) {
			out = append(out, i)
		}
	}
	return out
}

// markerLine is the body line row i's marker would land on.
func (m Model) markerLine(i int) int {
	probe := m
	// A fresh map: this is a question about a hypothetical cursor, and answering
	// it must not move the real one.
	probe.cursor = map[Tab]int{m.tab: i}
	return markedLine(strings.Split(probe.body(m.vp.Width), "\n"))
}

// markedLine is the line the cursor is on, or -1 when nothing is selected.
func markedLine(lines []string) int {
	for i, l := range lines {
		if marked(l) {
			return i
		}
	}
	return -1
}

// marked reports whether a line begins with the cursor's marker.
//
// Anchored to the start of the line rather than searched for anywhere in it: a
// file path or a shell command can contain the same character, and a row that
// merely mentions it is not the row the cursor is on.
func marked(line string) bool {
	return strings.HasPrefix(unstyled(line), cursorMark)
}

// unstyled drops the escape sequences a line opens with, so its first visible
// character can be read.
func unstyled(line string) string {
	for strings.HasPrefix(line, "\x1b[") {
		end := strings.IndexByte(line, 'm')
		if end < 0 {
			return line
		}
		line = line[end+1:]
	}
	return line
}

// sharePct keeps a decimal for small shares, where rounding to a whole number
// turns a real cost into "0%".
func sharePct(part, whole int) string {
	if whole <= 0 {
		return "0%"
	}
	pct := float64(part) / float64(whole) * 100
	if pct < 1 {
		return fmt.Sprintf("%.1f%%", pct)
	}
	return fmt.Sprintf("%.0f%%", pct)
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// failingHooks counts distinct failing hooks, not firings: one permanently
// broken hook fires every turn, and reporting "47 hooks failing" for a single
// misconfiguration would train the reader to ignore the badge.
//
// The count is all that leaves the hooks tab. The detail stays there, where it
// is looked for, rather than spending a header row on every other tab.
//
// Resolved and dismissed failures are excluded, which is the point of the badge:
// it should go quiet when there is nothing left to fix.
func (m Model) failingHooks() int {
	current, _, _ := m.failureGroups()
	return len(current)
}
