package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/attrib"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/event"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/session"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/transcript"
)

// Agent is one subagent spawned by the session under inspection.
//
// A subagent keeps its own complete transcript with its own usage, so its
// context can be broken down with exactly the same code as the parent's. That is
// the whole reason the analysis takes a line slice rather than a session.
type Agent struct {
	ID      string
	Type    string
	Started time.Time
	Ended   time.Time
	Running bool
	Report  attrib.Report
	// Analyzed distinguishes "this agent's window was empty" from "no transcript
	// was found for it", which look identical if both render as zero.
	Analyzed bool
	Requests int
	// Task is the human-readable description the parent gave when spawning it,
	// falling back to the opening line of the prompt it received.
	Task string
	// LastWrite is when the agent's transcript was last written, used only for
	// ordering. It is not a start time and must not be shown as one.
	LastWrite time.Time
	// ReplySize is the length of the agent's own final assistant message, read
	// from its transcript. Deliberately not the SubagentStop hook's
	// last_assistant_message — see transcript.LastAssistantText.
	ReplySize int
	// TranscriptPath is where this agent's transcript is, as the hook named it
	// rather than as it was guessed from the parent's path.
	TranscriptPath string
	// TxStart and TxEnd are the first and last timestamps in that transcript,
	// which is the only honest duration available while SubagentStart does not
	// fire.
	TxStart time.Time
	TxEnd   time.Time
}

// Label names the agent for display.
//
// SubagentStop does not in practice carry agent_type, whatever the hook
// reference says, so the type is read off the subagent's own transcript
// instead. The id is only a last resort — for an agent still running, which has
// no transcript to read yet.
func (a Agent) Label() string {
	if a.Type != "" {
		return a.Type
	}
	id := a.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return "agent " + id
}

// Elapsed is how long the agent ran, or has been running, and whether that is
// known at all.
//
// Events first, transcript second. SubagentStart is registered and handled but
// never actually fires — measured at zero occurrences against seventy-six stops
// — so pairing start against stop would leave this column permanently empty.
// The transcript's own first and last timestamps are real data and fill it.
//
// A file's mtime is still not used: it says when the agent finished and nothing
// about when it began, so a duration from it would be invented.
func (a Agent) Elapsed() (time.Duration, bool) {
	start, end := a.Started, a.Ended
	if start.IsZero() {
		start, end = a.TxStart, a.TxEnd
	}
	if a.Running {
		end = time.Now()
	}
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0, false
	}
	return end.Sub(start).Round(time.Second), true
}

// recency is whatever is known about when this agent was last active.
func (a Agent) recency() time.Time {
	for _, t := range []time.Time{a.Ended, a.TxEnd, a.Started, a.LastWrite} {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

// loadAgents pairs SubagentStop events with the transcripts on disk.
//
// Either source alone is insufficient: the events know whether an agent is still
// running, and the transcript knows its type, its task, and what its context
// actually holds.
//
// The join is on the path the event names for itself, not on a path rebuilt from
// the parent's. Rebuilding it was measured failing completely — thirty-one stop
// events and twenty-eight transcripts in one session with not a single id in
// common, because the harness had stopped leaving the files where the hook said
// it did. Taking the hook at its word survives that; guessing does not.
func loadAgents(s session.Session, events []event.Event, parent []transcript.Line) ([]Agent, int) {
	// What the parent asked for, keyed by the prompt it passed. This is the only
	// source of a task description written for a person rather than for a model.
	spawns := transcript.Spawns(parent)

	byID := map[string]*Agent{}
	// Transcripts an event named for itself, path to agent id.
	declared := map[string]string{}

	for _, ev := range events {
		if ev.Session != s.ID || ev.AgentID == "" {
			continue
		}
		a, ok := byID[ev.AgentID]
		if !ok {
			a = &Agent{ID: ev.AgentID, Type: ev.AgentType}
			byID[ev.AgentID] = a
		}
		if a.Type == "" {
			a.Type = ev.AgentType
		}
		if p := detailString(ev, "agent_transcript_path"); p != "" {
			a.TranscriptPath = filepath.Clean(p)
			declared[a.TranscriptPath] = ev.AgentID
		}
		switch ev.Event {
		case "SubagentStart":
			a.Started = ev.TS
			a.Running = true
		case "SubagentStop":
			a.Ended = ev.TS
			a.Running = false
		}
	}

	// Every transcript worth reading: the ones under the session's own
	// subagents/ directory, plus any an event named somewhere else.
	paths := map[string]bool{}
	dir := session.SubagentDir(s.Transcript)
	globbed, _ := filepath.Glob(filepath.Join(dir, "agent-*.jsonl"))
	for _, g := range globbed {
		paths[filepath.Clean(g)] = true
	}
	for d := range declared {
		if _, err := os.Stat(d); err == nil {
			paths[d] = true
		}
	}

	for path := range paths {
		// The event's id when there is one, since that is what the rest of the
		// events for this agent are keyed by. The filename only otherwise.
		id := declared[path]
		if id == "" {
			id = strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "agent-"), ".jsonl")
		}
		a, ok := byID[id]
		if !ok {
			// A subagent from before the hooks were installed: no events, but
			// its transcript is still readable and worth showing.
			a = &Agent{ID: id}
			byID[id] = a
		}
		lines, err := transcript.Load(path)
		if err != nil {
			continue
		}
		a.TranscriptPath = path
		a.Report = attrib.Analyze(lines, events)
		a.Analyzed = true
		a.Requests = a.Report.Turns
		a.ReplySize = len(transcript.LastAssistantText(lines))
		if st, en, ok := transcript.Span(lines); ok {
			a.TxStart, a.TxEnd = st, en
		}
		if ty := transcript.AgentType(lines); ty != "" {
			a.Type = ty
		}
		prompt := transcript.FirstPrompt(lines)
		if sp, ok := spawns[transcript.PromptKey(prompt)]; ok {
			a.Task = sp.Description
			if a.Type == "" {
				a.Type = sp.Type
			}
		}
		if a.Task == "" {
			a.Task = firstLine(prompt)
		}
		if a.LastWrite.IsZero() {
			if fi, err := os.Stat(path); err == nil {
				a.LastWrite = fi.ModTime()
			}
		}
	}

	// An agent with no transcript has nothing to show but eight characters of an
	// id. No type, no task, no context, no request count — a row of dashes that
	// reads as an agent which ran and returned nothing, when what is actually
	// missing is the file. Counted and said once, rather than listed.
	//
	// A running agent is kept: it has no transcript yet, and it is the thing you
	// are waiting on.
	out := make([]Agent, 0, len(byID))
	untraced := 0
	for _, a := range byID {
		if !a.Analyzed && !a.Running {
			untraced++
			continue
		}
		out = append(out, *a)
	}
	// Running agents first — they are what you are waiting on — then most
	// recent.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Running != out[j].Running {
			return out[i].Running
		}
		return out[i].recency().After(out[j].recency())
	})
	return out, untraced
}

// detailString reads one string out of an event's untyped detail.
func detailString(ev event.Event, key string) string {
	if ev.Detail == nil {
		return ""
	}
	s, _ := ev.Detail[key].(string)
	return s
}

// firstLine is a fallback task label: the opening sentence of the prompt an
// agent was given, when the parent recorded no description.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".\n"); i > 0 {
		s = s[:i]
	}
	return strings.Join(strings.Fields(s), " ")
}
