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
	// Largest-remainder apportionment. Naive truncation loses a column per
	// category and leaves a ragged gap at the end of the bar.
	type seg struct {
		bucket attrib.Bucket
		cells  int
		rem    float64
	}
	segs := make([]seg, 0, len(slices))
	used := 0
	for _, s := range slices {
		exact := float64(s.Tokens) / float64(total) * float64(width)
		cells := int(exact)
		segs = append(segs, seg{s.Bucket, cells, exact - float64(cells)})
		used += cells
	}
	for range width - used {
		best, bestRem := -1, -1.0
		for i := range segs {
			if segs[i].rem > bestRem {
				best, bestRem = i, segs[i].rem
			}
		}
		if best < 0 {
			break
		}
		segs[best].cells++
		segs[best].rem = -1
	}

	var b strings.Builder
	for _, s := range segs {
		if s.cells <= 0 {
			continue
		}
		b.WriteString(lipgloss.NewStyle().Foreground(colorFor(s.bucket)).
			Render(strings.Repeat("█", s.cells)))
	}
	return b.String()
}

// gauge draws overall window occupancy, colouring by how close to full it is.
// The threshold colours are the point: at 80% of the window, compaction is
// close enough to be worth reacting to.
func gauge(used, window, width int) string {
	if window <= 0 {
		window = used
	}
	if window <= 0 || width <= 0 {
		return ""
	}
	pct := float64(used) / float64(window)
	filled := int(pct * float64(width))
	if filled > width {
		filled = width
	}
	c := good
	switch {
	case pct > 0.8:
		c = bad
	case pct > 0.5:
		c = warn
	}
	return lipgloss.NewStyle().Foreground(c).Render(strings.Repeat("█", filled)) +
		faintStyle.Render(strings.Repeat("░", width-filled))
}

// miniBar is the per-row bar in the legend, drawn relative to the largest row
// rather than to the total. Scaling to the total makes every small category an
// indistinguishable blank.
func miniBar(tokens, largest, width int, b attrib.Bucket) string {
	if largest <= 0 || width <= 0 {
		return ""
	}
	n := tokens * width / largest
	if n == 0 && tokens > 0 {
		n = 1
	}
	return lipgloss.NewStyle().Foreground(colorFor(b)).Render(strings.Repeat("▊", n))
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
