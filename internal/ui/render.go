package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"claude-sidecar/internal/attrib"
)

// stackedBar draws the whole composition as one bar, each category in its own
// colour. This is the view's main claim: the shape of the bar is the answer to
// "what is filling the window", before reading a single number.
func stackedBar(slices []attrib.Slice, total, width int) string {
	if total <= 0 || width <= 0 {
		return strings.Repeat("─", max(width, 0))
	}
	sizes := make([]int, len(slices))
	for i, s := range slices {
		sizes[i] = s.Tokens
	}
	cells := apportion(sizes, total, width)

	var b strings.Builder
	for i, s := range slices {
		if cells[i] <= 0 {
			continue
		}
		b.WriteString(lipgloss.NewStyle().Foreground(colorFor(s.Bucket)).
			Render(strings.Repeat("█", cells[i])))
	}
	return b.String()
}

// apportion splits width cells among parts in proportion to their size.
//
// Largest-remainder, because naive truncation loses a cell per part and leaves a
// bar visibly short of the width it was given — with a dozen categories the gap
// at the end reads as missing data.
func apportion(parts []int, total, width int) []int {
	cells := make([]int, len(parts))
	if total <= 0 || width <= 0 {
		return cells
	}
	rem := make([]float64, len(parts))
	used := 0
	for i, n := range parts {
		exact := float64(n) / float64(total) * float64(width)
		cells[i] = int(exact)
		rem[i] = exact - float64(cells[i])
		used += cells[i]
	}
	for range width - used {
		best, bestRem := -1, -1.0
		for i := range rem {
			if rem[i] > bestRem {
				best, bestRem = i, rem[i]
			}
		}
		if best < 0 {
			break
		}
		cells[best]++
		rem[best] = -1
	}
	return cells
}

// stackedGauge draws the composition of the context window: the occupied part
// broken down by category in the same hues as the legend, the rest left faint.
//
// It replaces what used to be two separate bars — a plain fill gauge in the
// header and a composition bar in the body — which meant two adjacent,
// unlabelled bars encoding different things. One bar answers both questions:
// its length is how full the window is, its colours are what filled it.
func stackedGauge(slices []attrib.Slice, total, window, width int) string {
	if width <= 0 {
		return ""
	}
	if window <= 0 {
		window = total
	}
	if window <= 0 || total <= 0 {
		return faintStyle.Render(strings.Repeat("░", width))
	}

	used := int(float64(total) / float64(window) * float64(width))
	if used > width {
		used = width
	}
	// Never round a non-empty window down to nothing: a bar that reads as empty
	// while 20k tokens are in play is worse than one cell of overstatement.
	if used == 0 {
		used = 1
	}
	return stackedBar(slices, total, used) + faintStyle.Render(strings.Repeat("░", width-used))
}

// thresholdStyle tints a figure by how close the window is to full. At 80%,
// compaction is near enough to be worth reacting to; losing that warning was
// the one cost of dropping the old colour-changing gauge.
func thresholdStyle(total, window int) lipgloss.Style {
	if window <= 0 {
		return numStyle
	}
	switch pct := float64(total) / float64(window); {
	case pct > 0.8:
		return badStyle
	case pct > 0.5:
		return warnStyle
	default:
		return numStyle
	}
}

// miniBar is a per-row bar drawn relative to the largest row rather than to the
// total. Scaling to the total makes every small row an indistinguishable blank.
func miniBar(tokens, largest, width int, b attrib.Bucket) string {
	if largest <= 0 || width <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Foreground(colorFor(b)).
		Render(strings.Repeat("▊", barCells(tokens, largest, width)))
}

// segmentBar draws one row's bar broken into the parts that make it up, each in
// its own shade of the bucket's colour.
//
// Same length miniBar would have drawn, so a row that gets expanded does not
// also change size, and the same scale as every other bar in the table, so a
// child's own bar is exactly as wide as its segment here.
func segmentBar(children []attrib.Item, tokens, largest, width int, b attrib.Bucket) string {
	n := barCells(tokens, largest, width)
	if len(children) == 0 || n <= 0 {
		return miniBar(tokens, largest, width, b)
	}
	// Apportioned against what the children actually add up to, not against the
	// parent's own total. They are built to be the same number, and if they ever
	// are not, a bar that overshoots its cells wraps the table row.
	sizes, sum := make([]int, len(children)), 0
	for i, c := range children {
		sizes[i] = c.Tokens
		sum += c.Tokens
	}
	cells := apportion(sizes, sum, n)
	ramp := shades(b, len(children))

	var out strings.Builder
	for i := range children {
		if cells[i] <= 0 {
			continue
		}
		out.WriteString(lipgloss.NewStyle().Foreground(ramp[i]).
			Render(strings.Repeat("▊", cells[i])))
	}
	return out.String()
}

// barCells is a row's bar length. Anything with tokens gets at least one cell:
// rounding a real cost down to an empty bar reads as nothing at all.
func barCells(tokens, largest, width int) int {
	if largest <= 0 || width <= 0 {
		return 0
	}
	n := tokens * width / largest
	if n == 0 && tokens > 0 {
		n = 1
	}
	return n
}

// indent wraps text to a width and pushes every line of it in by the same
// margin. wrap alone indents the first line only, which reads as a hanging
// paragraph rather than as something nested.
func indent(text string, margin, width int) string {
	lead := strings.Repeat(" ", margin)
	var b strings.Builder
	for _, line := range strings.Split(wrap(text, maxInt(width-margin, 20)), "\n") {
		b.WriteString(lead + line + "\n")
	}
	return b.String()
}

// comma groups thousands. Context sizes run to seven figures and are unreadable
// without it.
func comma(n int) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var out []byte
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// compact renders a token count the way /context does, for tight columns.
func compact(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprint(n)
	}
}

// truncPath keeps the tail of a path: the basename identifies a file, the
// leading directories usually do not.
func truncPath(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n+1:])
}

// trunc keeps the head, for prose.
func trunc(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// pad right-pads to a display width, measuring with lipgloss so styled or
// wide-rune content lines up.
func pad(s string, n int) string {
	w := lipgloss.Width(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

// padLeft right-aligns, for number columns.
func padLeft(s string, n int) string {
	w := lipgloss.Width(s)
	if w >= n {
		return s
	}
	return strings.Repeat(" ", n-w) + s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// wrap folds text to a width on word boundaries. Explanatory notes read badly
// truncated, and in a column they no longer fit on one line.
func wrap(s string, width int) string {
	if width < 8 {
		return s
	}
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case lipgloss.Width(line)+1+lipgloss.Width(word) <= width:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
