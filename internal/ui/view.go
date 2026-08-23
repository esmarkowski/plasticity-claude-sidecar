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

	var b strings.Builder
	b.WriteString(m.header(inner))
	b.WriteString("\n")
	b.WriteString(m.tabs(inner))
	b.WriteString("\n")

	body := m.body(inner)
	b.WriteString(panel.Width(panelWidth(inner)).Render(body))
	b.WriteString("\n")
	b.WriteString(m.footer(inner))

	if m.picker {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			m.pickerView(), lipgloss.WithWhitespaceChars(" "))
	}
	return b.String()
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
		window = 1_000_000
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
	stat := fmt.Sprintf("%s  %s  %d requests", titleStyle.Render(tokens),
		dimStyle.Render(fmt.Sprintf("%.1f%%", pct)), r.Turns)
	gaugeW := w - lipgloss.Width(stat) - 2
	line2 := stat + "  " + gauge(r.Total, window, maxInt(gaugeW, 8))

	line3 := dimStyle.Render(truncPath(r.CWD, w))

	return panel.Width(panelWidth(w)).Render(line1 + "\n" + line2 + "\n" + line3)
}

func (m Model) tabs(w int) string {
	var parts []string
	for i, n := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, n)
		if Tab(i) == m.tab {
			parts = append(parts, chipOn.Render(label))
		} else {
			parts = append(parts, chipOff.Render(label))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
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

func (m Model) body(w int) string {
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
	return m.clip(out)
}

// clip applies the current tab's scroll offset and trims to the space the
// header, tabs, and footer left over.
func (m Model) clip(s string) string {
	lines := strings.Split(s, "\n")
	const chrome = 10 // header panel, tab row, body border, footer
	height := maxInt(m.height-chrome, 5)

	off := m.scroll[m.tab]
	if off > maxInt(len(lines)-height, 0) {
		off = maxInt(len(lines)-height, 0)
	}
	lines = lines[off:]
	if len(lines) > height {
		lines = lines[:height]
		lines = append(lines, dimStyle.Render("  ↓ more — j to scroll"))
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
	b.WriteString(stackedBar(r.Slices, r.Total, w))
	b.WriteString("\n\n")

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

// bucketDetail renders the drill-down rows behind one bucket.
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

	nameW := maxInt(w-34, 20)
	var b strings.Builder
	b.WriteString(faintStyle.Render(pad("", nameW)+padLeft("tokens", 10)+padLeft("share", 8)) + "\n")

	largest := slice.Detail[0].Tokens
	for _, it := range slice.Detail {
		name := it.Name
		if pathStyle {
			name = truncPath(name, nameW)
		} else {
			name = trunc(name, nameW)
		}
		pct := 0.0
		if slice.Tokens > 0 {
			pct = float64(it.Tokens) / float64(slice.Tokens) * 100
		}
		note := it.Note
		if it.Count > 1 {
			note = warnStyle.Render(fmt.Sprintf("×%d", it.Count)) + " " + dimStyle.Render(note)
		} else {
			note = dimStyle.Render(note)
		}
		fmt.Fprintf(&b, "%s%s%s  %s %s\n",
			pad(name, nameW),
			padLeft(numStyle.Render(comma(it.Tokens)), 10),
			padLeft(dimStyle.Render(fmt.Sprintf("%.0f%%", pct)), 8),
			miniBar(it.Tokens, largest, 10, bucket),
			note,
		)
	}
	b.WriteString("\n" + faintStyle.Render(fmt.Sprintf("%d entries · %s tokens total",
		len(slice.Detail), comma(slice.Tokens))))
	return b.String()
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
