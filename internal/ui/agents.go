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
	ReplySize int
}

// Label names the agent for display.
//
// SubagentStop does not in practice carry agent_type, whatever the hook
// reference says, so the type is read off the subagent's own transcript
// instead. The id is only a last resort, for an agent that left no transcript.
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
// It is only knowable from SubagentStart/Stop events. For an agent found by its
// transcript alone the start time is the file's mtime, which is when it
// *finished* — reporting a duration from that would be inventing one.
func (a Agent) Elapsed() (time.Duration, bool) {
	if a.Started.IsZero() {
		return 0, false
	}
	end := a.Ended
	if a.Running {
		end = time.Now()
	}
	if end.IsZero() || end.Before(a.Started) {
		return 0, false
	}
	return end.Sub(a.Started).Round(time.Second), true
}

// recency is whatever is known about when this agent was last active.
func (a Agent) recency() time.Time {
	if !a.Ended.IsZero() {
		return a.Ended
	}
	if !a.Started.IsZero() {
		return a.Started
	}
	return a.LastWrite
}

// loadAgents pairs SubagentStart/Stop events with the transcripts on disk.
//
// Either source alone is insufficient: the events know the agent's type and
// whether it is still running, and the transcript knows what its context
// actually holds.
func loadAgents(s session.Session, events []event.Event, parent []transcript.Line) []Agent {
	// What the parent asked for, keyed by the prompt it passed. This is the only
	// source of a task description written for a person rather than for a model.
	spawns := transcript.Spawns(parent)

	byID := map[string]*Agent{}

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
		switch ev.Event {
		case "SubagentStart":
			a.Started = ev.TS
			a.Running = true
		case "SubagentStop":
			a.Ended = ev.TS
			a.Running = false
			a.ReplySize = ev.Sizes["last_assistant_message"]
		}
	}

	dir := session.SubagentDir(s.Transcript)
	paths, _ := filepath.Glob(filepath.Join(dir, "agent-*.jsonl"))
	for _, p := range paths {
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "agent-"), ".jsonl")
		a, ok := byID[id]
		if !ok {
			// A subagent from before the hooks were installed: no events, but
			// its transcript is still readable and worth showing.
			a = &Agent{ID: id}
			byID[id] = a
		}
		lines, err := transcript.Load(p)
		if err != nil {
			continue
		}
		a.Report = attrib.Analyze(lines, events)
		a.Analyzed = true
		a.Requests = a.Report.Turns
		if t := transcript.AgentType(lines); t != "" {
			a.Type = t
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
			if fi, err := os.Stat(p); err == nil {
				a.LastWrite = fi.ModTime()
			}
		}
	}

	out := make([]Agent, 0, len(byID))
	for _, a := range byID {
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
	return out
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
