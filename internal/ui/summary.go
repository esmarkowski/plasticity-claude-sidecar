package ui

import (
	"os"
	"sync"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/attrib"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/event"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/harness"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/session"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/transcript"
)

// summary is just enough of a session's attribution to draw its bar in the
// picker: how full the window is, and what filled it.
type summary struct {
	Total  int
	Window int
	Model  string
	Title  string
	Slices []attrib.Slice
}

// pickerSessions bounds how many sessions get analyzed. The picker only shows a
// screenful, and analyzing every transcript on disk would mean parsing tens of
// megabytes to render rows nobody sees.
const pickerSessions = 12

// summaryCache keeps one analysis per transcript, invalidated by size and mtime.
//
// Without it, every refresh would re-parse every session in the list. With it,
// steady state re-parses only the session actually being written to, which is
// the one whose numbers are changing anyway.
var summaryCache = struct {
	sync.Mutex
	entries map[string]cachedSummary
}{entries: map[string]cachedSummary{}}

type cachedSummary struct {
	size    int64
	modUnix int64
	summary summary
}

// summarize analyzes the head of the session list, reusing cached results for
// transcripts that have not changed.
//
// Called from the refresh command, off the render loop, so a slow first pass
// costs latency on the numbers rather than on keystrokes.
func summarize(sessions []session.Session, events []event.Event) map[string]summary {
	out := make(map[string]summary, len(sessions))
	for i, s := range sessions {
		if i >= pickerSessions {
			break
		}
		if sum, ok := summaryFor(s, events); ok {
			out[s.ID] = sum
		}
	}
	return out
}

func summaryFor(s session.Session, events []event.Event) (summary, bool) {
	fi, err := os.Stat(s.Transcript)
	if err != nil {
		return summary{}, false
	}

	summaryCache.Lock()
	cached, ok := summaryCache.entries[s.Transcript]
	summaryCache.Unlock()
	if ok && cached.size == fi.Size() && cached.modUnix == fi.ModTime().UnixNano() {
		return cached.summary, true
	}

	lines, err := transcript.Load(s.Transcript)
	if err != nil {
		return summary{}, false
	}
	// A stale snapshot is fine and a probe is not an option here: probing starts
	// a Claude Code session, and doing that per row of a session list would be
	// absurd.
	snap, _ := harness.Get(s.CWD, false, 0)
	rep := attrib.AnalyzeWith(lines, events, snap)

	sum := summary{Total: rep.Total, Window: rep.Window, Model: rep.Model, Title: rep.Title, Slices: rep.Slices}
	summaryCache.Lock()
	summaryCache.entries[s.Transcript] = cachedSummary{
		size: fi.Size(), modUnix: fi.ModTime().UnixNano(), summary: sum,
	}
	summaryCache.Unlock()
	return sum, true
}

// withContent drops sessions that never sent a request.
//
// `sidecar probe` starts a throwaway Claude Code session to read /context, and
// every one of those lands in the transcript directory looking like the most
// recently active session there is. Left in, they clutter the picker and — worse
// — `--follow` steers the dashboard onto one the moment a probe runs.
//
// A session with no usage has nothing to show either way, so this is not a
// special case for probes: it is a filter for sessions with no content.
func withContent(sessions []session.Session, summaries map[string]summary) []session.Session {
	out := make([]session.Session, 0, len(sessions))
	for _, s := range sessions {
		if sum, known := summaries[s.ID]; known && sum.Total == 0 {
			continue
		}
		out = append(out, s)
	}
	// Never return nothing: on a fresh install every session may be empty, and
	// an empty picker is less useful than an uninformative one.
	if len(out) == 0 {
		return sessions
	}
	return out
}

// sessionName prefers the session's own title over its directory and uuid.
//
// A session is not anonymous — it has a name if one was given at launch, and a
// generated one otherwise — but nothing about the filesystem layout suggests
// that, so the picker used to identify sessions by eight hex characters.
func sessionName(s session.Session, sum summary) string {
	if sum.Title != "" {
		return sum.Title
	}
	return s.Label()
}

// sessionNames labels a list of sessions, disambiguating any that share a name.
//
// Collisions are ordinary rather than rare: resuming work in the same directory
// on the same task tends to produce the same generated title, and two rows
// reading "debug-context-window-visibility" identify nothing.
// The width matters: the disambiguating suffix has to survive truncation, so
// the name is shortened to make room for it rather than appended and then cut.
func sessionNames(sessions []session.Session, summaries map[string]summary, width int) []string {
	names := make([]string, len(sessions))
	seen := map[string]int{}
	for i, s := range sessions {
		names[i] = sessionName(s, summaries[s.ID])
		seen[names[i]]++
	}
	for i, s := range sessions {
		if seen[names[i]] < 2 {
			names[i] = trunc(names[i], width)
			continue
		}
		id := s.ID
		if len(id) > 8 {
			id = id[:8]
		}
		suffix := " · " + id
		names[i] = trunc(names[i], maxInt(width-len(suffix), 4)) + suffix
	}
	return names
}
