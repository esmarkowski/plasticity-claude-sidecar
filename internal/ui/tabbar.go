package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// tabBar is the chip row.
//
// It owns its own hit testing because a click arrives as a pair of coordinates
// and nothing else in the layout knows where the chips were drawn. Bubbles has
// no tabs component to borrow — it ships list, table, and viewport, none of
// which is a row of chips — so this is the smallest thing that can answer
// "which tab is under the pointer".
type tabBar struct {
	row string
	// y is the terminal row the chips were drawn on, and spans are their column
	// ranges. Both are recorded at render time, since that is the only moment
	// the geometry is known.
	y     int
	spans []span
}

// span is a half-open column range: from is inside, to is not.
type span struct{ from, to int }

func (s span) holds(x int) bool { return x >= s.from && x < s.to }

// tabsBar lays out the chip row and records where every chip landed.
//
// Two labellings, widest first. The chip row is the one piece of chrome that
// cannot wrap without shifting the whole layout down a line, so it degrades to
// numbers rather than overflowing — the active tab keeps its name, since that is
// the one you need to read.
func (m Model) tabsBar(w int) tabBar {
	failing := m.failingHooks()

	for _, full := range []bool{true, false} {
		var parts []string
		var spans []span
		at := 0
		for i, name := range tabNames {
			label := fmt.Sprint(i + 1)
			if full || Tab(i) == m.tab {
				label += " " + name
			}
			if Tab(i) == TabHooks && failing > 0 {
				label += fmt.Sprintf(" ✗%d", failing)
			}
			chip := chipOff.Render(label)
			if Tab(i) == m.tab {
				chip = chipOn.Render(label)
			}
			parts = append(parts, chip)
			spans = append(spans, span{at, at + lipgloss.Width(chip)})
			at += lipgloss.Width(chip)
		}
		row := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
		if lipgloss.Width(row) <= w || !full {
			return tabBar{row: row, spans: spans}
		}
	}
	return tabBar{}
}

// hit names the tab at a point, if the point is on the chip row at all.
func (t tabBar) hit(x, y int) (Tab, bool) {
	if y != t.y {
		return 0, false
	}
	for i, s := range t.spans {
		if s.holds(x) {
			return Tab(i), true
		}
	}
	return 0, false
}
