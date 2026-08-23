package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"claude-sidecar/internal/attrib"
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
	// A rounded panel costs 2 columns of border and 2 of padding, and lipgloss
	// sizes a style by its content box. Getting this wrong by even one column
	// makes every bar wrap onto a second line.
	inner := m.width - panelChrome

	header := m.header(inner)
	tabs := m.tabs(inner)
	footer := m.footer(inner)

	// Measure the chrome rather than assuming it. The header grows a row when
	// hooks are failing, and a hardcoded height would silently eat the last line
	// of every tab whenever it did.
	chrome := lipgloss.Height(header) + lipgloss.Height(tabs) + lipgloss.Height(footer) + panelBorder
	body := panel.Width(panelWidth(inner)).Render(m.body(inner, m.height-chrome))

	if m.picker {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			m.pickerView(m.width), lipgloss.WithWhitespaceChars(" "))
	}
	return strings.Join([]string{header, tabs, body, footer}, "\n")
}

// header is always visible: which session, how full, and under what settings.
// Everything here answers "am I looking at the thing I think I am".
func (m Model) header(w int) string {
	r := m.report

	name := m.current.Label()
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

func (m Model) tabs(w int) string {
	failing := m.failingHooks()

	// Two labellings, widest first. The chip row is the one piece of chrome that
	// cannot wrap without shifting the whole layout down a line, so it degrades
	// to numbers rather than overflowing — the active tab keeps its name, since
	// that is the one you need to read.
	for _, full := range []bool{true, false} {
		var parts []string
		for i, name := range tabNames {
			label := fmt.Sprint(i + 1)
			if full || Tab(i) == m.tab {
				label += " " + name
			}
			if Tab(i) == TabHooks && failing > 0 {
				label += fmt.Sprintf(" ✗%d", failing)
			}
			if Tab(i) == m.tab {
				parts = append(parts, chipOn.Render(label))
			} else {
				parts = append(parts, chipOff.Render(label))
			}
		}
		row := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
		if lipgloss.Width(row) <= w || !full {
			return row
		}
	}
	return ""
}

func (m Model) footer(w int) string {
	live := goodStyle.Render("● live")
	switch {
	case m.err != nil:
		live = badStyle.Render("● " + trunc(m.err.Error(), 40))
	case !m.report.Probed:
		live = warnStyle.Render("● estimated · sidecar probe")
	}

	// Drop hints from the right until the row fits. A footer that wraps pushes
	// the whole layout up by a line and makes the dashboard jitter as the
	// status text changes length.
	full := []string{"1-6 tabs", "s session", "p pin", "j/k scroll", "r refresh", "q quit"}
	for n := len(full); n > 0; n-- {
		left := helpStyle.Render(strings.Join(full[:n], "  ·  "))
		if lipgloss.Width(left)+lipgloss.Width(live)+1 <= w {
			return pad(left, w-lipgloss.Width(live)) + live
		}
	}
	return padLeft(live, w)
}

func (m Model) body(w, h int) string {
	if m.err != nil && m.report.Total == 0 {
		return m.emptyView()
	}
	var out string
	switch m.tab {
	case TabContext:
		out = m.contextView(w)
	case TabRules:
		out = m.rulesView(w)
	case TabTools:
		out = m.toolsView(w)
	case TabAgents:
		out = m.agentsView(w)
	case TabHooks:
		out = m.hooksView(w)
	case TabTimeline:
		out = m.timelineView(w)
	}
	return m.clip(out, h)
}

// clip applies the current tab's scroll offset and trims to the space the
// header, tabs, and footer left over.
func (m Model) clip(s string, h int) string {
	lines := strings.Split(s, "\n")
	height := maxInt(h, 5)

	off := m.scroll[m.tab]
	if max := maxInt(len(lines)-height, 0); off > max {
		off = max
	}

	// Being scrolled has to be visible. Without this the view is indistinguishable
	// from a short tab, and content above the fold — which is where anything
	// urgent is put — silently does not exist. A persisted scroll offset made
	// that permanent across restarts.
	var head, tail string
	if off > 0 {
		head = warnStyle.Render(fmt.Sprintf("  ↑ %d lines above — g for top", off))
		height--
	}
	lines = lines[off:]
	if len(lines) > height {
		lines = lines[:height]
		tail = dimStyle.Render("  ↓ more — j to scroll")
	}
	if head != "" {
		lines = append([]string{head}, lines...)
	}
	if tail != "" {
		lines = append(lines, tail)
	}
	return strings.Join(lines, "\n")
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
	barW := maxInt(w-swatchW-nameW-numW-pctW-gapW, 6)

	for _, s := range r.Slices {
		pct := 0.0
		if r.Total > 0 {
			pct = float64(s.Tokens) / float64(r.Total) * 100
		}
		swatch := lipgloss.NewStyle().Foreground(colorFor(s.Bucket)).Render("▐")
		fmt.Fprintf(&b, "%s %s%s%s %s\n",
			swatch,
			pad(string(s.Bucket), nameW),
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

// bucketDetail renders the drill-down rows behind one bucket: what, how much,
// and what share of the category.
//
// Bars are scaled to the largest row rather than to the window, which is what
// the share column is for. Scaled to the window every row here would be a
// sliver, since a single category is a fraction of the context to begin with.
func bucketDetail(r attrib.Report, bucket attrib.Bucket, w int, pathStyle bool) string {
	var slice *attrib.Slice
	for i := range r.Slices {
		if r.Slices[i].Bucket == bucket {
			slice = &r.Slices[i]
			break
		}
	}
	if slice == nil || len(slice.Detail) == 0 {
		return dimStyle.Render("nothing in this category yet")
	}

	const (
		numW   = 10
		shareW = 7
		countW = 6
		gapW   = 1
	)
	// Size the name column to the content, not to the panel: tool names are
	// short, and a column stretched to the full width pushes every number off to
	// the right where they cannot be compared.
	nameW := 12
	for _, it := range slice.Detail {
		if n := lipgloss.Width(it.Name) + 1; n > nameW {
			nameW = n
		}
	}
	// A note column only when there is something to put in it. The rules tab
	// uses it for why a file was loaded, which is the whole reason that tab
	// exists; the tools tab has nothing to say there.
	noteW := 0
	for _, it := range slice.Detail {
		if it.Note == "" {
			continue
		}
		if n := lipgloss.Width(it.Note) + 2; n > noteW {
			noteW = n
		}
	}
	noteW = minInt(noteW, 22)

	nameW = minInt(nameW, maxInt(w-numW-shareW-countW-noteW-gapW-10, 14))
	barW := maxInt(w-nameW-numW-shareW-countW-noteW-gapW, 6)

	var b strings.Builder
	header := pad("", nameW) + padLeft("tokens", numW) + padLeft("share", shareW) + padLeft("uses", countW)
	if noteW > 0 {
		header += pad("  loaded", noteW)
	}
	b.WriteString(faintStyle.Render(header) + "\n")

	largest := slice.Detail[0].Tokens
	for _, it := range slice.Detail {
		name := trunc(it.Name, nameW-1)
		if pathStyle {
			name = truncPath(it.Name, nameW-1)
		}
		share := 0.0
		if slice.Tokens > 0 {
			share = float64(it.Tokens) / float64(slice.Tokens) * 100
		}
		count := dimStyle.Render("—")
		if it.Count > 1 {
			count = warnStyle.Render(fmt.Sprintf("×%d", it.Count))
		}
		row := pad(name, nameW) +
			padLeft(numStyle.Render(comma(it.Tokens)), numW) +
			padLeft(dimStyle.Render(fmt.Sprintf("%.0f%%", share)), shareW) +
			padLeft(count, countW)
		if noteW > 0 {
			row += pad("  "+dimStyle.Render(trunc(it.Note, noteW-2)), noteW)
		}
		b.WriteString(row + " " + miniBar(it.Tokens, largest, barW, bucket) + "\n")
	}

	b.WriteString("\n" + faintStyle.Render(fmt.Sprintf("%d entries · %s tokens · %s of context",
		len(slice.Detail), comma(slice.Tokens), sharePct(slice.Tokens, r.Total))))
	return b.String()
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
func (m Model) failingHooks() int {
	return len(groupFailures(m.hooks))
}
