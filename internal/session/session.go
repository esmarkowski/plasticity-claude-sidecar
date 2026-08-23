// Package session finds the Claude Code sessions worth showing.
package session

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"claude-sidecar/internal/event"
)

// Session is one Claude Code conversation the dashboard can display.
type Session struct {
	ID         string
	Transcript string
	CWD        string
	Slug       string
	Active     time.Time
}

// Label is what the session picker shows: the working directory basename is far
// more recognizable than a UUID when several sessions are running.
func (s Session) Label() string {
	name := filepath.Base(s.CWD)
	if name == "." || name == "" || name == string(filepath.Separator) {
		name = s.Slug
	}
	short := s.ID
	if len(short) > 8 {
		short = short[:8]
	}
	return name + " · " + short
}

// ProjectsDir is where Claude Code keeps transcripts.
func ProjectsDir() string {
	if d := os.Getenv("CLAUDE_PROJECTS_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "projects"
	}
	return filepath.Join(home, ".claude", "projects")
}

// List returns known sessions, most recently active first.
//
// Events are the better source — they carry the session id, cwd, and transcript
// path directly — but the dashboard has to work when it is started mid-session
// before any hook has fired, so the transcript directory is scanned too.
func List(events []event.Event) []Session {
	byID := map[string]*Session{}

	for _, path := range transcripts() {
		id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		byID[id] = &Session{
			ID:         id,
			Transcript: path,
			Slug:       filepath.Base(filepath.Dir(path)),
			CWD:        unslug(filepath.Base(filepath.Dir(path))),
			Active:     fi.ModTime(),
		}
	}

	for _, ev := range events {
		if ev.Session == "" {
			continue
		}
		s, ok := byID[ev.Session]
		if !ok {
			s = &Session{ID: ev.Session}
			byID[ev.Session] = s
		}
		if ev.CWD != "" {
			s.CWD = ev.CWD
		}
		if ev.Transcript != "" {
			s.Transcript = ev.Transcript
		}
		if ev.TS.After(s.Active) {
			s.Active = ev.TS
		}
	}

	out := make([]Session, 0, len(byID))
	for _, s := range byID {
		if s.Transcript == "" {
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Active.After(out[j].Active) })
	return out
}

// Active returns the session to follow: whichever one moved most recently.
func Active(events []event.Event) (Session, bool) {
	all := List(events)
	if len(all) == 0 {
		return Session{}, false
	}
	return all[0], true
}

// Find returns a session by id or by unambiguous id prefix.
func Find(events []event.Event, id string) (Session, bool) {
	all := List(events)
	for _, s := range all {
		if s.ID == id {
			return s, true
		}
	}
	for _, s := range all {
		if strings.HasPrefix(s.ID, id) {
			return s, true
		}
	}
	return Session{}, false
}

// SubagentDir is where a session's subagent transcripts live. Each subagent
// keeps its own full transcript with its own usage, so the same attribution
// code works on it unchanged.
func SubagentDir(transcriptPath string) string {
	return filepath.Join(
		filepath.Dir(transcriptPath),
		strings.TrimSuffix(filepath.Base(transcriptPath), ".jsonl"),
		"subagents",
	)
}

func transcripts() []string {
	entries, err := os.ReadDir(ProjectsDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(ProjectsDir(), e.Name(), "*.jsonl"))
		if err != nil {
			continue
		}
		out = append(out, matches...)
	}
	return out
}

// unslug turns Claude Code's directory name back into a path. The encoding is
// lossy — every separator became a dash, and so did any dash in a directory
// name — so this is only ever a fallback label for a session whose real cwd we
// have not seen in an event.
func unslug(slug string) string {
	return strings.ReplaceAll(slug, "-", "/")
}
