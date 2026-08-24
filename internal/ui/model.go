// Package ui is the live dashboard.
package ui

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/attrib"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/event"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/harness"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/memory"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/session"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/transcript"
)

// Tab is one view of the session.
type Tab int

const (
	TabContext Tab = iota
	TabRules
	TabTools
	TabAgents
	TabHooks
	TabTimeline
)

var tabNames = []string{"context", "rules", "tools", "agents", "hooks", "timeline"}

func (t Tab) String() string { return tabNames[t] }

// Model is the dashboard state.
type Model struct {
	width, height int

	events   []event.Event
	sessions []session.Session
	current  session.Session
	pinned   bool

	report   attrib.Report
	audit    []attrib.AuditPoint
	snapshot harness.Snapshot
	agents   []Agent
	hooks    []transcript.HookRun
	memory   memory.Store
	summary  map[string]summary

	tab    Tab
	picker bool
	pick   int

	// vp scrolls the body. Its own key map is left unbound: the arrows and j/k
	// are the cursor's, and letting the viewport claim them too would move both
	// at once. Paging and the mouse wheel are routed to it explicitly.
	vp viewport.Model
	// bar is the chip row, kept from the last layout so a click can be tested
	// against where the chips actually were.
	bar tabBar
	// bodyTop is the screen row the body's first content line is drawn on.
	bodyTop int
	// probing makes every row draw the cursor's marker, so one render reports
	// where all of them ended up. Never set on the Model that gets displayed.
	probing bool
	// offset remembers each tab's scroll position, because one viewport is shared
	// by all of them and switching tabs would otherwise carry the offset over.
	offset map[Tab]int
	// chase asks the next layout to bring the cursor into view. Set only by the
	// keys that move the cursor: following it on every refresh meant scrolling
	// the timeline by wheel and being yanked back two seconds later, because the
	// cursor was still parked on the first row.
	chase bool

	// cursor is the selected row per tab. Every tab that draws rows has one, so
	// j/k means the same thing throughout and only falls back to scrolling on a
	// tab with nothing to select.
	cursor map[Tab]int
	// expanded holds the rows whose breakdown is showing, keyed by the row.
	//
	// Tracking what is open rather than what is shut is what makes shut the
	// default: a breakdown is detail asked for, and every row volunteering its
	// own made the tools tab forty lines of things nobody had asked about.
	expanded map[rowRef]bool

	// dismissed watermarks hook failures the user has acknowledged: key to the
	// failure's most recent firing at the time it was dismissed. Anything newer
	// than the watermark is a fresh failure and shows again.
	dismissed map[string]time.Time

	err      error
	lastLoad time.Time
	watcher  *watcher

	// follow selects the most recently active session on every refresh, which
	// is what makes the dashboard track whatever the user is actually doing.
	follow bool
}

// State is the slice of UI state worth surviving a hot-reload restart. Without
// it every rebuild would dump you back on the first tab, which turns a reload
// into an interruption.
type State struct {
	Tab     int         `json:"tab"`
	Scroll  map[int]int `json:"scroll"`
	Session string      `json:"session"`
	Pinned  bool        `json:"pinned"`
	// Dismissed has to outlive the process: a hook failure dismissed before a
	// rebuild that came back after it would make the dismissal useless.
	Dismissed map[string]time.Time `json:"dismissed,omitempty"`
	// Cursor and Expanded are the same bet the scroll offset makes: a rebuild
	// should put you back on the row you were reading, opened as you left it.
	Cursor   int         `json:"cursor,omitempty"`
	Expanded [][2]string `json:"expanded,omitempty"`
}

// New builds the dashboard.
func New(follow bool, sessionID string, restored State) Model {
	m := Model{
		tab:       Tab(restored.Tab),
		offset:    map[Tab]int{},
		cursor:    map[Tab]int{Tab(restored.Tab): restored.Cursor},
		expanded:  map[rowRef]bool{},
		follow:    follow,
		pinned:    restored.Pinned,
		dismissed: restored.Dismissed,
	}
	for _, r := range restored.Expanded {
		m.expanded[rowRef{r[0], r[1]}] = true
	}
	for k, v := range restored.Scroll {
		m.offset[Tab(k)] = v
	}
	m.vp = newViewport()
	m.vp.YOffset = m.offset[m.tab]
	if sessionID != "" {
		m.current = session.Session{ID: sessionID}
		m.follow = false
	} else if restored.Session != "" && restored.Pinned {
		m.current = session.Session{ID: restored.Session}
	}
	return m
}

// SaveState returns what should be written before exit.
//
// Only the active tab's offset is kept. Persisting every tab's position meant a
// deep offset from one long tab outlived the content that justified it, and
// restored you below whatever the tab was trying to tell you.
func (m Model) SaveState() State {
	scroll := map[int]int{}
	if off := m.vp.YOffset; off > 0 {
		scroll[int(m.tab)] = off
	}
	var expanded [][2]string
	for ref, on := range m.expanded {
		if on {
			expanded = append(expanded, [2]string{ref.Kind, ref.Name})
		}
	}
	// Sorted so a saved state file does not churn on every exit.
	sort.Slice(expanded, func(i, j int) bool {
		if expanded[i][0] != expanded[j][0] {
			return expanded[i][0] < expanded[j][0]
		}
		return expanded[i][1] < expanded[j][1]
	})
	return State{Tab: int(m.tab), Scroll: scroll, Session: m.current.ID, Pinned: m.pinned,
		Dismissed: m.dismissed, Cursor: m.cursor[m.tab], Expanded: expanded}
}

// newViewport is the body's scroller.
//
// Its key map is cleared: the viewport binds the arrows, j/k, and space by
// default, and the dashboard needs those for the cursor and for enter. Paging
// and the mouse wheel are routed to it explicitly instead.
func newViewport() viewport.Model {
	vp := viewport.New(0, 0)
	vp.KeyMap = viewport.KeyMap{}
	return vp
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.load(), tick()}
	if m.watcher != nil {
		cmds = append(cmds, m.watcher.next())
	}
	return tea.Batch(cmds...)
}

// loadedMsg carries a completed refresh. Analysis happens off the render loop
// because a large transcript takes long enough to parse that doing it inline
// would make keystrokes feel sticky.
type loadedMsg struct {
	events   []event.Event
	sessions []session.Session
	current  session.Session
	report   attrib.Report
	audit    []attrib.AuditPoint
	snapshot harness.Snapshot
	agents   []Agent
	hooks    []transcript.HookRun
	memory   memory.Store
	summary  map[string]summary
	err      error
}

func (m Model) load() tea.Cmd {
	want := m.current.ID
	follow := m.follow && !m.pinned
	return func() tea.Msg { return analyze(want, follow) }
}

// analyze does the whole refresh synchronously. Shared by the render loop's
// command and by --once, so the one-shot render exercises the same code path
// the live view does.
func analyze(want string, follow bool) loadedMsg {
	{
		evs, err := event.Load(event.LogPath())
		if err != nil {
			return loadedMsg{err: err}
		}
		sessions := session.List(evs)
		if len(sessions) == 0 {
			return loadedMsg{events: evs, err: errNoSessions}
		}

		// Summaries first, because whether a session is worth listing depends on
		// whether anything was ever sent in it.
		summaries := summarize(sessions, evs)
		sessions = withContent(sessions, summaries)

		cur := sessions[0]
		if !follow && want != "" {
			s, ok := session.Find(evs, want)
			if !ok {
				// Falling back to another session here is how you end up
				// confidently reading the wrong one's numbers.
				return loadedMsg{events: evs, sessions: sessions, summary: summaries,
					err: fmt.Errorf("no session matching %q — press s to choose one", want)}
			}
			cur = s
		}

		lines, err := transcript.Load(cur.Transcript)
		if err != nil {
			return loadedMsg{events: evs, sessions: sessions, current: cur, err: err}
		}

		// A stale harness snapshot is used rather than probing: a probe starts
		// a Claude Code session, which is not something a refresh should do.
		snap, _ := harness.Get(cur.CWD, false, 0)

		rep := attrib.AnalyzeWith(lines, evs, snap)
		rep.Session = cur.ID
		if rep.CWD == "" {
			rep.CWD = cur.CWD
		}
		return loadedMsg{
			events:   evs,
			sessions: sessions,
			current:  cur,
			report:   rep,
			audit:    attrib.Audit(lines, evs, snap),
			snapshot: snap,
			agents:   loadAgents(cur, evs, lines),
			// Hook failures come from the transcript rather than our own log:
			// a hook that fails to start never writes anything, so only Claude
			// Code's own record of it exists.
			hooks: transcript.Hooks(lines),
			// Read off disk rather than out of the transcript, which has nothing
			// to say about it. The harness names the store's location; see
			// internal/memory.
			memory:  memory.Load(memory.Dir(snap.Memory, cur.Transcript)),
			summary: summaries,
		}
	}
}

// Render produces a single frame. Used by `watch --once`, which is how the
// layout gets checked without a terminal — in CI, in a pipe, or from an agent
// that has no TTY to attach to.
func Render(follow bool, sessionID string, restored State, width, height int, picker bool) string {
	m := New(follow, sessionID, restored)
	m.width, m.height = width, height
	m.picker = picker
	updated, _ := m.Update(analyze(m.current.ID, m.follow))
	return updated.View()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.refresh()
		return m, nil

	case tea.MouseMsg:
		return m.mouse(msg)

	case tea.KeyMsg:
		updated, cmd := m.key(msg)
		next := updated.(Model)
		next.refresh()
		return next, cmd

	case tickMsg:
		return m, tea.Batch(m.load(), tick())

	case fileChangedMsg:
		var cmds []tea.Cmd
		cmds = append(cmds, m.load())
		if m.watcher != nil {
			cmds = append(cmds, m.watcher.next())
		}
		return m, tea.Batch(cmds...)

	case loadedMsg:
		m.events = msg.events
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.sessions = msg.sessions
		m.current = msg.current
		m.report = msg.report
		m.audit = msg.audit
		m.snapshot = msg.snapshot
		m.agents = msg.agents
		m.hooks = msg.hooks
		m.memory = msg.memory
		m.summary = msg.summary
		m.lastLoad = time.Now()
		m.refresh()
		// Watch the new session's directories: following a session means
		// following its files.
		if m.watcher != nil {
			m.watcher.add(dirOf(m.current.Transcript))
			m.watcher.add(session.SubagentDir(m.current.Transcript))
		}
		return m, nil
	}
	return m, nil
}

func (m Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.picker {
		switch msg.String() {
		case "esc", "s", "q":
			m.picker = false
		case "up", "k":
			m.pick = maxInt(0, m.pick-1)
		case "down", "j":
			m.pick = minInt(len(m.sessions)-1, m.pick+1)
		case "enter":
			if m.pick < len(m.sessions) {
				m.current = m.sessions[m.pick]
				m.pinned = true
				m.picker = false
				return m, m.load()
			}
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "1", "2", "3", "4", "5", "6":
		m = m.goTo(Tab(msg.String()[0] - '1'))
	// Tab and shift+tab move between tabs; the arrows belong to the rows. Both
	// were bound to tab switching before, which left no key for the tree.
	case "tab":
		m = m.goTo(Tab((int(m.tab) + 1) % len(tabNames)))
	case "shift+tab":
		m = m.goTo(Tab((int(m.tab) - 1 + len(tabNames)) % len(tabNames)))
	case "down", "j":
		m = m.move(1)
	case "up", "k":
		m = m.move(-1)
	case "right", "l":
		return m.expand()
	case "left", "h":
		return m.collapse()
	case "pgdown", "ctrl+d":
		m.vp.HalfPageDown()
	case "pgup", "ctrl+u":
		m.vp.HalfPageUp()
	case "g", "home":
		m.cursor, m.chase = withCursor(m.cursor, m.tab, 0), true
	case "G", "end":
		m.cursor = withCursor(m.cursor, m.tab, maxInt(len(m.selectableRows())-1, 0))
		m.chase = true
	case "enter", " ":
		return m.activate()
	case "s":
		m.picker = true
		m.pick = 0
	case "p":
		m.pinned = !m.pinned
		if !m.pinned {
			return m, m.load()
		}
	case "r":
		return m, m.load()
	case "x":
		// Only offered where the failures are listed: a dismissal you cannot see
		// the effect of is indistinguishable from a keystroke that did nothing.
		if m.tab == TabHooks {
			m.dismissed = m.dismissAll()
		}
	case "X":
		if m.tab == TabHooks {
			m.dismissed = nil
		}
	}
	return m, nil
}

// move walks the cursor, or scrolls when the tab has no rows to walk.
//
// The scroll offset follows rather than leads: clip works out where the cursor
// landed and brings it into view, so moving the cursor never has to know how
// many lines a row happens to occupy.
func (m Model) move(delta int) Model {
	rows := m.selectableRows()
	if len(rows) == 0 {
		m.vp.LineDown(delta)
		if delta < 0 {
			m.vp.LineUp(-delta)
		}
		return m
	}
	m.cursor = withCursor(m.cursor, m.tab,
		minInt(maxInt(m.cursor[m.tab]+delta, 0), len(rows)-1))
	m.chase = true
	return m
}

// goTo switches tabs, parking the offset of the one being left. One viewport is
// shared by every tab, so without this a deep scroll on one tab carries over to
// the next and lands you below its content.
func (m Model) goTo(t Tab) Model {
	parked := map[Tab]int{}
	for k, v := range m.offset {
		parked[k] = v
	}
	parked[m.tab] = m.vp.YOffset
	m.offset = parked
	m.tab = t
	m.vp.SetYOffset(parked[t])
	return m
}

// expand opens the selected row, and falls through to activate for a row with
// nothing to open — right on a category is still "show me this".
func (m Model) expand() (tea.Model, tea.Cmd) {
	ref, ok := m.selected()
	if ok && m.hasBreakdown(ref) && !m.expanded[ref] {
		return m.setOpen(ref, true), nil
	}
	if ok && m.hasBreakdown(ref) {
		return m, nil
	}
	return m.activate()
}

// collapse closes the selected row. Nothing to do on a row that has no
// breakdown: left is not a way out of the tab.
func (m Model) collapse() (tea.Model, tea.Cmd) {
	ref, ok := m.selected()
	if !ok || !m.hasBreakdown(ref) || !m.expanded[ref] {
		return m, nil
	}
	return m.setOpen(ref, false), nil
}

// setOpen sets a row's open state on a copy of the map. Bubble Tea keeps the
// previous Model, and a shared map would edit that one too.
func (m Model) setOpen(ref rowRef, open bool) Model {
	next := map[rowRef]bool{}
	for k, v := range m.expanded {
		next[k] = v
	}
	next[ref] = open
	m.expanded = next
	return m
}

// mouse handles the pointer: the wheel scrolls the body, and a click on the chip
// row switches tabs. Anything else is left alone rather than guessed at.
func (m Model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	e := tea.MouseEvent(msg)
	if e.Action != tea.MouseActionPress || e.Button != tea.MouseButtonLeft {
		// The viewport owns wheel handling, including how far one notch goes.
		vp, cmd := m.vp.Update(msg)
		m.vp = vp
		return m, cmd
	}
	if t, ok := m.bar.hit(e.X, e.Y); ok {
		next := m.goTo(t)
		next.refresh()
		return next, nil
	}
	return m.click(e.X, e.Y)
}

// click selects the row under the pointer, and opens or shuts it when the
// pointer is on its fold marker — the marker is drawn as an affordance, so it
// should behave like one.
func (m Model) click(x, y int) (tea.Model, tea.Cmd) {
	line := y - m.bodyTop + m.vp.YOffset
	if line < 0 {
		return m, nil
	}
	i, ok := m.rowAt(line)
	if !ok {
		return m, nil
	}
	m.cursor = withCursor(m.cursor, m.tab, i)

	ref, _ := m.selected()
	if onFoldMark(x) && m.hasBreakdown(ref) {
		m = m.setOpen(ref, !m.expanded[ref])
	}
	m.refresh()
	return m, nil
}

// panelInset is the left border and padding a panel puts before its content, so
// a screen column can be read as a body column.
const panelInset = 2

// onFoldMark reports whether a screen column falls on the fold marker, which
// sits just past the cursor's own gutter.
func onFoldMark(x int) bool {
	at := x - panelInset - gutter
	return at >= 0 && at < markerW
}

// withCursor sets a tab's cursor on a copy. Bubble Tea keeps the previous Model,
// and a shared map would move its cursor too.
func withCursor(cur map[Tab]int, tab Tab, i int) map[Tab]int {
	next := map[Tab]int{}
	for k, v := range cur {
		next[k] = v
	}
	next[tab] = i
	return next
}

// activate is what enter does to the selected row, which depends on what the row
// is: a row with a breakdown folds, a category on the context tab jumps to the
// tab that details it, and a hook failure is dismissed.
func (m Model) activate() (tea.Model, tea.Cmd) {
	ref, ok := m.selected()
	if !ok {
		return m, nil
	}
	switch {
	case ref.Kind == kindBucket:
		if tab, ok := detailTab[attrib.Bucket(ref.Name)]; ok {
			m.tab = tab
		}
	case ref.Kind == kindHook:
		m.dismissed = m.dismissOne(ref.Name)
	case m.hasBreakdown(ref):
		m = m.setOpen(ref, !m.expanded[ref])
	}
	return m, nil
}

// detailTab is the tab that breaks a category down, for the categories that have
// one. Jumping there is the obvious thing to want from a row on the context tab.
var detailTab = map[attrib.Bucket]Tab{
	attrib.BucketRules:       TabRules,
	attrib.BucketToolResults: TabTools,
	attrib.BucketToolCalls:   TabTools,
	attrib.BucketHookOutput:  TabHooks,
	attrib.BucketAgents:      TabAgents,
}

// hasBreakdown reports whether a row has anything folded under it. Asked of
// every kind of row, since the fold state is keyed for all of them.
func (m Model) hasBreakdown(ref rowRef) bool {
	switch ref.Kind {
	case kindAgent:
		for _, a := range m.agents {
			if a.ID == ref.Name {
				return len(a.Report.Slices) > 0
			}
		}
	case kindRequest:
		for _, p := range m.audit {
			if fmt.Sprint(p.Request) == ref.Name {
				return len(p.Detail) > 0
			}
		}
	default:
		for _, s := range m.report.Slices {
			if string(s.Bucket) != ref.Kind {
				continue
			}
			for _, it := range s.Detail {
				if it.Name == ref.Name {
					return len(it.Children) > 0
				}
			}
		}
	}
	return false
}

// open reports whether a row's breakdown should be drawn: it has one, and it has
// been asked for.
func (m Model) open(ref rowRef) bool {
	return m.hasBreakdown(ref) && m.expanded[ref]
}

// dismissAll marks every failure the hooks tab is currently showing as seen.
func (m Model) dismissAll() map[string]time.Time { return m.dismiss(nil) }

// dismissOne marks a single failure as seen, which is what enter does on a
// selected row.
func (m Model) dismissOne(key string) map[string]time.Time {
	return m.dismiss(map[string]bool{key: true})
}

// dismiss watermarks failures as seen — all of them, or only the keys given.
//
// Watermarked at the failure's last firing rather than at the wall clock, so
// recognizing a later failure as new does not depend on the two agreeing.
func (m Model) dismiss(only map[string]bool) map[string]time.Time {
	out := map[string]time.Time{}
	for k, v := range m.dismissed {
		out[k] = v
	}
	for _, f := range groupFailures(m.hooks) {
		if only != nil && !only[f.key()] {
			continue
		}
		at := f.Last
		if at.IsZero() {
			// No timestamps in this transcript; the clock is all there is.
			at = time.Now()
		}
		out[f.key()] = at
	}
	return out
}

// Attach wires the file watcher in. Kept separate from New so the caller owns
// the watcher's lifetime and can close it on shutdown.
func (m *Model) Attach(w *watcher) { m.watcher = w }

// Watch builds a watcher over everything the dashboard reads.
func Watch() (*watcher, error) {
	dirs := []string{event.Dir(), session.ProjectsDir()}
	entries, err := os.ReadDir(session.ProjectsDir())
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, session.ProjectsDir()+string(os.PathSeparator)+e.Name())
			}
		}
	}
	return newWatcher(dirs...)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
