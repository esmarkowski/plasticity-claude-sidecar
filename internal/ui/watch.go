package ui

import (
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"
)

// fileChangedMsg says something the dashboard reads has moved on disk.
type fileChangedMsg struct{}

// tickMsg is the safety net behind the watcher.
type tickMsg time.Time

// watcher turns filesystem notifications into Bubble Tea messages.
//
// It watches directories rather than files. A transcript is appended to in
// place, which fsnotify does report, but Claude Code also creates new session
// files and subagent directories mid-run, and a watch on a file that did not
// exist when the dashboard started never fires.
type watcher struct {
	fsw    *fsnotify.Watcher
	events chan struct{}
}

func newWatcher(dirs ...string) (*watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &watcher{fsw: fsw, events: make(chan struct{}, 1)}
	for _, d := range dirs {
		_ = fsw.Add(d)
	}
	go w.loop()
	return w, nil
}

// add registers another directory, for a session whose project directory was
// not known at startup.
func (w *watcher) add(path string) {
	if path == "" || w == nil {
		return
	}
	_ = w.fsw.Add(path)
}

func (w *watcher) loop() {
	// Coalesce: a single turn produces a burst of writes across the transcript
	// and the event log, and re-analyzing on each one would burn CPU to
	// redraw the same frame.
	var pending bool
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		select {
		case _, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if !pending {
				pending = true
				timer.Reset(120 * time.Millisecond)
			}
		case <-timer.C:
			pending = false
			select {
			case w.events <- struct{}{}:
			default:
			}
		case _, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
		}
	}
}

// Close releases the filesystem watch.
func (w *watcher) Close() {
	if w != nil && w.fsw != nil {
		_ = w.fsw.Close()
	}
}

// next blocks until the watcher reports a change.
func (w *watcher) next() tea.Cmd {
	return func() tea.Msg {
		<-w.events
		return fileChangedMsg{}
	}
}

// tick is a slow fallback poll. fsnotify is reliable for appends on macOS, but
// a missed notification would silently freeze the dashboard, and a stale view
// that looks live is worse than a slow one.
func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func dirOf(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Dir(path)
}
