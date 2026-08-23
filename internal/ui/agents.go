package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"claude-sidecar/internal/attrib"
	"claude-sidecar/internal/event"
	"claude-sidecar/internal/session"
	"claude-sidecar/internal/transcript"
)

// Agent is one subagent spawned by the session under inspection.
//
// A subagent keeps its own complete transcript with its own usage, so its
// context can be broken down with exactly the same code as the parent's. That is
// the whole reason the analysis takes a line slice rather than a session.
type Agent struct {
	ID        string
	Type      string
	Started   time.Time
	Ended     time.Time
	Running   bool
	Report    attrib.Report
	Requests  int
	ReplySize int
}

// Elapsed is how long the agent ran, or has been running.
func (a Agent) Elapsed() time.Duration {
	end := a.Ended
	if a.Running {
		end = time.Now()
	}
	if a.Started.IsZero() || end.Before(a.Started) {
		return 0
	}
	return end.Sub(a.Started).Round(time.Second)
}

// loadAgents pairs SubagentStart/Stop events with the transcripts on disk.
//
// Either source alone is insufficient: the events know the agent's type and
// whether it is still running, and the transcript knows what its context
// actually holds.
func loadAgents(s session.Session, events []event.Event) []Agent {
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
			// A subagent from a session that predates the hooks: no events, but
			// its transcript is still readable and worth showing.
			a = &Agent{ID: id, Type: "(before hooks)"}
			byID[id] = a
		}
		lines, err := transcript.Load(p)
		if err != nil {
			continue
		}
		a.Report = attrib.Analyze(lines, events)
		a.Requests = a.Report.Turns
		if a.Started.IsZero() {
			if fi, err := os.Stat(p); err == nil {
				a.Started = fi.ModTime()
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
		return out[i].Started.After(out[j].Started)
	})
	return out
}
