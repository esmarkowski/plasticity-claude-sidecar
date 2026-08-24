package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/attrib"
)

// A bar that is short of the width it was given reads as missing data, so every
// cell has to be handed out.
func TestApportionUsesEveryCell(t *testing.T) {
	parts := []int{7, 5, 3, 1, 1}
	total := 17
	for width := 1; width <= 80; width++ {
		cells := apportion(parts, total, width)
		sum := 0
		for _, c := range cells {
			sum += c
		}
		if sum != width {
			t.Fatalf("width %d: cells sum to %d", width, sum)
		}
	}
}

func TestApportionDegenerateInputs(t *testing.T) {
	for _, c := range apportion([]int{1, 2}, 0, 10) {
		if c != 0 {
			t.Error("apportioned against a zero total")
		}
	}
	if got := apportion([]int{1, 2}, 3, 0); len(got) != 2 || got[0] != 0 || got[1] != 0 {
		t.Errorf("apportioned into zero width: %v", got)
	}
}

// Expanding a row must not resize it: the segments are the same bar, so the
// column stays comparable to the rows above and below.
func TestSegmentBarIsTheSameLengthAsAPlainOne(t *testing.T) {
	children := []attrib.Item{
		{Name: "git", Tokens: 500},
		{Name: "gh", Tokens: 300},
		{Name: "other", Tokens: 200},
	}
	for _, tokens := range []int{1000, 400, 25, 1} {
		for _, width := range []int{6, 12, 40, 64} {
			plain := lipgloss.Width(miniBar(tokens, 1000, width, attrib.BucketToolCalls))
			seg := lipgloss.Width(segmentBar(children, tokens, 1000, width, attrib.BucketToolCalls))
			if plain != seg {
				t.Errorf("tokens %d width %d: plain bar %d cells, segmented %d",
					tokens, width, plain, seg)
			}
		}
	}
}

// A row with nothing to break down still gets a bar.
func TestSegmentBarFallsBackWithoutChildren(t *testing.T) {
	got := segmentBar(nil, 500, 1000, 10, attrib.BucketToolResults)
	if lipgloss.Width(got) != 5 {
		t.Errorf("bar = %q, want 5 cells", got)
	}
}

// The shades are what tie a row in the list to its segment in the bar. Two rows
// sharing a shade would make the bar unreadable.
func TestShadesAreDistinct(t *testing.T) {
	ramp := shades(attrib.BucketToolCalls, 6)
	if len(ramp) != 6 {
		t.Fatalf("got %d shades, want 6", len(ramp))
	}
	seen := map[string]bool{}
	for _, c := range ramp {
		key := c.Light + "/" + c.Dark
		if seen[key] {
			t.Errorf("duplicate shade %s", key)
		}
		seen[key] = true
	}
	// The first shade is the bucket's own colour, so an unexpanded bar and an
	// expanded one start in the same place.
	if base := colorFor(attrib.BucketToolCalls); ramp[0] != base {
		t.Errorf("first shade %v is not the bucket colour %v", ramp[0], base)
	}
}

func TestShadesHandlesOne(t *testing.T) {
	if got := shades(attrib.BucketRules, 1); len(got) != 1 {
		t.Errorf("got %d shades for n=1", len(got))
	}
	if got := shades(attrib.BucketRules, 0); len(got) != 1 {
		t.Errorf("got %d shades for n=0, want a usable ramp", len(got))
	}
}

func TestBlendMovesTowardTheTarget(t *testing.T) {
	if got := blend("#000000", "#FFFFFF", 0); got != "#000000" {
		t.Errorf("blend at 0 = %s", got)
	}
	if got := blend("#000000", "#FFFFFF", 1); got != "#FFFFFF" {
		t.Errorf("blend at 1 = %s", got)
	}
	if got := blend("#000000", "#FFFFFF", 0.5); got != "#7F7F7F" {
		t.Errorf("blend at half = %s", got)
	}
	// A colour we cannot read is a flat bar, not a crash.
	if got := blend("rebeccapurple", "#FFFFFF", 0.5); got != "rebeccapurple" {
		t.Errorf("unparseable colour = %s", got)
	}
}

// The name column must not move when the nested rows change. Sizing it to them
// meant the column grew the first time a long command appeared under Bash, and
// every number in the table stepped sideways mid-refresh — which is what "the
// numbers keep shifting" was.
func TestLayoutIgnoresNestedNames(t *testing.T) {
	parent := attrib.Item{Name: "Bash", Tokens: 100}
	narrow := layout([]attrib.Item{withKids(parent, "git")}, 120)
	wide := layout([]attrib.Item{withKids(parent, "some-very-long-command-name")}, 120)

	if narrow != wide {
		t.Errorf("a longer nested name moved the columns: %+v then %+v", narrow, wide)
	}
	// Top-level names do size it, since those are what the table is a list of.
	longer := layout([]attrib.Item{{Name: "SomeVeryLongToolName", Tokens: 100}}, 120)
	if longer.name <= narrow.name {
		t.Errorf("a longer tool name did not widen the column: %d vs %d", longer.name, narrow.name)
	}
}

func withKids(it attrib.Item, names ...string) attrib.Item {
	for _, n := range names {
		it.Children = append(it.Children, attrib.Item{Name: n, Tokens: 100 / len(names)})
	}
	return it
}

// Every row of the table has to line up, nested or not.
func TestBucketDetailKeepsColumnsAligned(t *testing.T) {
	r := attrib.Report{Total: 1000, Slices: []attrib.Slice{{
		Bucket: attrib.BucketToolCalls,
		Tokens: 600,
		Detail: []attrib.Item{
			{Name: "Bash", Tokens: 400, Count: 30, Children: []attrib.Item{
				{Name: "git", Tokens: 250, Count: 20},
				{Name: "gh", Tokens: 100, Count: 8},
				{Name: "other", Tokens: 50, Count: 2},
			}},
			{Name: "Read", Tokens: 200, Count: 4},
		},
	}}}
	m := Model{report: r, cursor: map[Tab]int{}, tab: TabTools,
		expanded: map[rowRef]bool{{string(attrib.BucketToolCalls), "Bash"}: true}}
	out := m.bucketDetail(attrib.BucketToolCalls, 100, false)

	// Where the bar starts is where every column before it ended, so one shared
	// offset across the tools and the commands under them is the alignment.
	starts := map[int]bool{}
	rows := 0
	for _, line := range strings.Split(out, "\n") {
		plain := ansiPattern.ReplaceAllString(line, "")
		if !strings.Contains(plain, "▊") {
			continue
		}
		rows++
		starts[barStart(plain)] = true
	}
	if rows != 5 {
		t.Fatalf("got %d bars, want two tools and three commands", rows)
	}
	if len(starts) != 1 {
		t.Errorf("bars start at %d different columns, want them aligned: %v", len(starts), starts)
	}
	if !strings.Contains(out, "git") || !strings.Contains(out, "gh") {
		t.Error("the breakdown did not render")
	}
}

// ansiPattern strips styling, so a test can measure where a column lands.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// barStart is the column a row's bar begins at. The last run of blocks on the
// line, since a nested row also carries a swatch in its name column.
func barStart(plain string) int {
	r := []rune(plain)
	i := len(r) - 1
	for i >= 0 && r[i] == '▊' {
		i--
	}
	return i + 1
}

// The bar on the parent row is the one making the point; the ones under it are
// its parts, and at full strength a breakdown reads as six competing bars.
func TestNestedBarsAreFainterThanTheirSwatch(t *testing.T) {
	swatch := shades(attrib.BucketToolResults, 4)[0]
	bar := fade(swatch, nestedFade)
	if bar == swatch {
		t.Fatal("the nested bar is the same colour as the row's swatch")
	}

	// Toward white under a light theme and black under a dark one, which is what
	// makes it read as faded either way rather than as some other colour.
	base, _ := parseHex(swatch.Light)
	faded, _ := parseHex(bar.Light)
	for i := range base {
		if faded[i] < base[i] {
			t.Errorf("light channel %d moved away from the background: %v to %v", i, base, faded)
		}
	}
	base, _ = parseHex(swatch.Dark)
	faded, _ = parseHex(bar.Dark)
	for i := range base {
		if faded[i] > base[i] {
			t.Errorf("dark channel %d moved away from the background: %v to %v", i, base, faded)
		}
	}

	// Not so far that it is gone: the last part of a long breakdown fades twice,
	// once down the ramp and once for being nested.
	last := fade(shades(attrib.BucketToolResults, 6)[5], nestedFade)
	if last == fade(colorFor(attrib.BucketToolResults), 1) {
		t.Error("the last nested bar faded into the background")
	}
}

// The timeline's column exists so the cause of a jump can be read without
// expanding every row to find it.
func TestCauseNamesTheLargestThingItCan(t *testing.T) {
	p := attrib.AuditPoint{Detail: []attrib.Item{
		{Name: "unexplained", Tokens: 900},
		{Name: "Read user.rb", Tokens: 400},
		{Name: "thinking", Tokens: 100},
	}}
	if got := cause(p); got != "Read user.rb" {
		t.Errorf("cause = %q, want the command rather than the remainder", got)
	}

	// Nothing nameable is nothing said, not "other".
	only := attrib.AuditPoint{Detail: []attrib.Item{
		{Name: "unexplained", Tokens: 900},
		{Name: "other", Tokens: 40},
	}}
	if got := cause(only); got != "" {
		t.Errorf("cause = %q, want nothing", got)
	}
	if got := cause(attrib.AuditPoint{}); got != "" {
		t.Errorf("cause of a request with no detail = %q", got)
	}
}

// The marker is anchored to the start of a line because content can contain the
// same character: a shell command, or a path with a guillemet in it. A row that
// merely mentions it is not the row the cursor is on.
func TestMarkedIgnoresTheMarkerInContent(t *testing.T) {
	if !marked(accentStyle.Render(cursorMark) + " Bash git") {
		t.Error("a marked row was not recognised")
	}
	if marked("  Bash echo a › b") {
		t.Error("a row whose content contains the marker was read as selected")
	}
	if marked("") || marked("  Read") {
		t.Error("an unmarked row was read as selected")
	}
	// Styling in front of the marker must not hide it, and styling in front of
	// content must not reveal one that is not there.
	if marked(dimStyle.Render("  Read user.rb")) {
		t.Error("a styled unmarked row was read as selected")
	}
}
