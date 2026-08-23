// Package ui is the live dashboard.
package ui

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"claude-sidecar/internal/attrib"
	"claude-sidecar/internal/event"
	"claude-sidecar/internal/harness"
	"claude-sidecar/internal/session"
	"claude-sidecar/internal/transcript"
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
	summary  map[string]summary

	tab    Tab
	scroll map[Tab]int
	picker bool
	pick   int

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
}

// New builds the dashboard.
func New(follow bool, sessionID string, restored State) Model {
	m := Model{
		tab:    Tab(restored.Tab),
		scroll: map[Tab]int{},
		follow: follow,
		pinned: restored.Pinned,
	}
	for k, v := range restored.Scroll {
		m.scroll[Tab(k)] = v
	}
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
	if off := m.scroll[m.tab]; off > 0 {
		scroll[int(m.tab)] = off
	}
	return State{Tab: int(m.tab), Scroll: scroll, Session: m.current.ID, Pinned: m.pinned}
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
			agents:   loadAgents(cur, evs),
			// Hook failures come from the transcript rather than our own log:
			// a hook that fails to start never writes anything, so only Claude
			// Code's own record of it exists.
			hooks:   transcript.Hooks(lines),
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
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)

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
		m.summary = msg.summary
		m.lastLoad = time.Now()
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
		m.tab = Tab(msg.String()[0] - '1')
	case "tab", "right", "l":
		m.tab = Tab((int(m.tab) + 1) % len(tabNames))
	case "shift+tab", "left", "h":
		m.tab = Tab((int(m.tab) - 1 + len(tabNames)) % len(tabNames))
	case "down", "j":
		m.scroll[m.tab]++
	case "up", "k":
		m.scroll[m.tab] = maxInt(0, m.scroll[m.tab]-1)
	case "g":
		m.scroll[m.tab] = 0
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
	}
	return m, nil
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
