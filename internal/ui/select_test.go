package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"claude-sidecar/internal/attrib"
	"claude-sidecar/internal/session"
)

func toolsModel() Model {
	m := Model{
		tab:       TabTools,
		width:     100,
		height:    40,
		offset:    map[Tab]int{},
		cursor:    map[Tab]int{},
		collapsed: map[rowRef]bool{},
		vp:        newViewport(),
		report: attrib.Report{Total: 1000, Slices: []attrib.Slice{
			{Bucket: attrib.BucketToolResults, Tokens: 600, Detail: []attrib.Item{
				{Name: "Bash", Tokens: 400, Count: 30, Children: []attrib.Item{
					{Name: "git", Tokens: 250, Count: 20},
					{Name: "gh", Tokens: 150, Count: 8},
				}},
				{Name: "Read", Tokens: 200, Count: 4},
			}},
			{Bucket: attrib.BucketToolCalls, Tokens: 100, Detail: []attrib.Item{
				{Name: "Edit", Tokens: 100, Count: 3},
			}},
		}},
	}
	return m
}

// The cursor runs across both of the tools tab's tables, because that is how the
// rows are drawn and j/k should not stop halfway down the panel.
func TestSelectableRowsSpanBothTables(t *testing.T) {
	got := toolsModel().selectableRows()
	want := []rowRef{
		{string(attrib.BucketToolResults), "Bash"},
		{string(attrib.BucketToolResults), "Read"},
		{string(attrib.BucketToolCalls), "Edit"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A tab with nothing to select has to leave j/k alone, or the key does nothing at
// all there.
func TestTabsWithoutRowsFallBackToScrolling(t *testing.T) {
	m := toolsModel()
	m.tab = TabTimeline // no audit points loaded
	if rows := m.selectableRows(); len(rows) != 0 {
		t.Fatalf("timeline reported %d selectable rows with no requests", len(rows))
	}
	m.vp.Height = 5
	m.vp.SetContent(strings.Repeat("row\n", 40))
	m = m.move(1)
	if m.vp.YOffset != 1 {
		t.Errorf("j did not scroll a tab with no rows: offset %d", m.vp.YOffset)
	}
	if m.cursor[TabTimeline] != 0 {
		t.Errorf("a tab with no rows moved its cursor to %d", m.cursor[TabTimeline])
	}
}

func TestMoveClampsToTheEnds(t *testing.T) {
	m := toolsModel()
	for range 10 {
		m = m.move(1)
	}
	if got := m.cursor[TabTools]; got != 2 {
		t.Errorf("cursor ran past the last row to %d", got)
	}
	for range 10 {
		m = m.move(-1)
	}
	if got := m.cursor[TabTools]; got != 0 {
		t.Errorf("cursor ran past the first row to %d", got)
	}
}

// The rows are sorted by size and reorder under the cursor on every refresh, so
// the selection is held by name.
func TestSelectionSurvivesAShorterList(t *testing.T) {
	m := toolsModel()
	m.cursor[TabTools] = 2
	if ref, _ := m.selected(); ref.Name != "Edit" {
		t.Fatalf("selected %+v", ref)
	}
	// The tool calls table goes away, leaving fewer rows than the cursor index.
	m.report.Slices = m.report.Slices[:1]
	ref, ok := m.selected()
	if !ok || ref.Name != "Read" {
		t.Errorf("stale cursor landed on %+v, want the last remaining row", ref)
	}
}

// Enter folds the breakdown away, and the row keeps its own bar.
func TestEnterFoldsABreakdown(t *testing.T) {
	m := toolsModel()
	if !strings.Contains(m.bucketDetail(attrib.BucketToolResults, 100, false), "git") {
		t.Fatal("the breakdown was not open to begin with")
	}

	updated, _ := m.activate()
	m = updated.(Model)
	out := m.bucketDetail(attrib.BucketToolResults, 100, false)
	if strings.Contains(out, "git") {
		t.Error("enter did not fold the breakdown away")
	}
	if !strings.Contains(out, "▸") {
		t.Error("a folded row does not show it can be unfolded")
	}

	updated, _ = m.activate()
	if !strings.Contains(updated.(Model).bucketDetail(attrib.BucketToolResults, 100, false), "git") {
		t.Error("enter did not unfold it again")
	}
}

// Folding must not reach back into the Model it was cloned from: Bubble Tea keeps
// the previous value, and a shared map would edit it too.
func TestFoldingDoesNotMutateThePreviousModel(t *testing.T) {
	m := toolsModel()
	updated, _ := m.activate()
	if m.collapsed[rowRef{string(attrib.BucketToolResults), "Bash"}] {
		t.Error("the fold leaked backwards into the model it came from")
	}
	if !updated.(Model).collapsed[rowRef{string(attrib.BucketToolResults), "Bash"}] {
		t.Error("the fold did not land on the new model")
	}
}

// Enter on a category jumps to the tab that breaks it down, which is the only
// thing there is to want from that row.
func TestEnterOnACategoryOpensItsTab(t *testing.T) {
	m := toolsModel()
	m.tab = TabContext
	// Slices are drawn in report order; the first is tool results.
	updated, _ := m.activate()
	if got := updated.(Model).tab; got != TabTools {
		t.Errorf("landed on tab %v, want tools", got)
	}
}

// The viewport has to follow the cursor, or selecting a row below the fold
// selects something you cannot see.
func TestViewportFollowsTheCursor(t *testing.T) {
	m := toolsModel()
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = "row"
	}
	lines[50] = cursorMark + " selected"

	m.vp.Height = 20
	m.vp.SetContent(strings.Join(lines, "\n"))
	m.scrollToCursor(lines)
	if m.vp.YOffset != 50-20+1 {
		t.Errorf("offset %d does not bring line 50 into a 20-line view", m.vp.YOffset)
	}

	// And it leaves the offset alone when the cursor is already in view, so
	// keystrokes do not fight the mouse wheel.
	lines[50] = "row"
	lines[5] = cursorMark + " selected"
	m.vp.SetYOffset(0)
	m.scrollToCursor(lines)
	if m.vp.YOffset != 0 {
		t.Errorf("scrolled to %d for a cursor that was already visible", m.vp.YOffset)
	}
}

// Every tab shares the one viewport, so its offset has to be parked and restored
// or a deep scroll on one tab lands you below the content of the next.
func TestSwitchingTabsKeepsEachOffset(t *testing.T) {
	m := toolsModel()
	m.vp.Height = 10
	m.vp.SetContent(strings.Repeat("row\n", 100))
	m.vp.SetYOffset(40)

	m = m.goTo(TabAgents)
	if m.vp.YOffset != 0 {
		t.Errorf("carried offset %d onto a freshly opened tab", m.vp.YOffset)
	}
	m.vp.SetContent(strings.Repeat("row\n", 100))
	m = m.goTo(TabTools)
	if m.vp.YOffset != 40 {
		t.Errorf("came back to offset %d, want the 40 it was left at", m.vp.YOffset)
	}
}

// Right opens a row and left shuts it; neither is a way out of the tab.
func TestArrowsOpenAndShutARow(t *testing.T) {
	m := toolsModel()
	ref := rowRef{string(attrib.BucketToolResults), "Bash"}

	shut, _ := m.collapse()
	m = shut.(Model)
	if !m.collapsed[ref] {
		t.Fatal("left did not shut the row")
	}
	open, _ := m.expand()
	m = open.(Model)
	if m.collapsed[ref] {
		t.Error("right did not reopen the row")
	}

	// Left on a row with nothing to shut changes nothing at all.
	m.cursor[TabTools] = 1 // Read, no children
	same, _ := m.collapse()
	if same.(Model).tab != TabTools {
		t.Error("left moved off the tab")
	}
}

// A click lands on the tab whose chip is under it, and nothing at all when it
// misses the row.
func TestTabBarHitTesting(t *testing.T) {
	m := toolsModel()
	bar := m.tabsBar(120)
	bar.y = 4
	if len(bar.spans) != len(tabNames) {
		t.Fatalf("laid out %d chips for %d tabs", len(bar.spans), len(tabNames))
	}
	for i := range tabNames {
		mid := (bar.spans[i].from + bar.spans[i].to) / 2
		if got, ok := bar.hit(mid, 4); !ok || got != Tab(i) {
			t.Errorf("click at x=%d hit %v (ok=%v), want tab %d", mid, got, ok, i)
		}
	}
	if _, ok := bar.hit(bar.spans[0].from, 9); ok {
		t.Error("a click on another row hit the chips")
	}
	if _, ok := bar.hit(10_000, 4); ok {
		t.Error("a click past the last chip hit something")
	}
}

// A smoke test through Update, because refresh touches every map on the Model
// and a nil one there is a panic that takes the whole dashboard down.
func TestUpdateSurvivesTheKeysAndTheMouse(t *testing.T) {
	m := New(false, "", State{})
	var model tea.Model = m
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model, _ = model.Update(loadedMsg{
		report:  toolsModel().report,
		current: session.Session{ID: "s"},
	})

	for _, k := range []string{"tab", "shift+tab", "3", "j", "j", "k", "right", "left",
		"enter", " ", "pgdown", "pgup", "G", "g", "x", "X", "1", "6"} {
		model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		model, _ = model.Update(keyOf(k))
	}
	for _, e := range []tea.MouseMsg{
		{X: 3, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft},
		{X: 3, Y: 20, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft},
		{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown},
		{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp},
	} {
		model, _ = model.Update(e)
	}
	if out := model.View(); out == "" {
		t.Error("the dashboard rendered nothing after all that")
	}
}

// Scrolling has to survive the two-second reload. Following the cursor on every
// refresh dragged the view back to wherever the cursor was parked, which on the
// timeline is the first request — so scrolling to the bottom lasted one tick.
func TestARefreshDoesNotUndoScrolling(t *testing.T) {
	m := New(false, "", State{})
	var model tea.Model = m
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 20})

	// The timeline, because that is where this was seen: a long list whose cursor
	// sits on the first request.
	points := make([]attrib.AuditPoint, 200)
	for i := range points {
		points[i] = attrib.AuditPoint{Request: i + 1, Context: 1000 * (i + 1)}
	}
	load := loadedMsg{report: toolsModel().report, audit: points,
		current: session.Session{ID: "s"}}
	model, _ = model.Update(load)
	model, _ = model.Update(keyOf("6"))

	// Scroll away from the top the way the wheel does.
	scrolled := model.(Model)
	scrolled.vp.SetYOffset(120)
	parked := scrolled.vp.YOffset
	if parked == 0 {
		t.Fatal("the body is not long enough for this test to mean anything")
	}

	model, _ = scrolled.Update(load)
	if got := model.(Model).vp.YOffset; got != parked {
		t.Errorf("a refresh moved the view from %d to %d", parked, got)
	}

	// But moving the cursor still pulls the view to it.
	model, _ = model.(Model).Update(keyOf("k"))
	if got := model.(Model).vp.YOffset; got == parked {
		t.Error("moving the cursor did not bring it back into view")
	}
}
