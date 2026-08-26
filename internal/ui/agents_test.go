package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/event"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/session"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/transcript"
)

// agentTranscript writes a subagent transcript at path: a prompt, a reply, and
// timestamps far enough apart to measure.
func agentTranscript(t *testing.T, path, prompt, reply string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","uuid":"u1","parentUuid":null,"timestamp":"2026-08-23T20:03:12Z",` +
		`"attributionAgent":"code-reviewer",` +
		`"message":{"role":"user","content":[{"type":"text","text":"` + prompt + `"}]}}` + "\n" +
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","timestamp":"2026-08-23T20:04:12Z",` +
		`"message":{"role":"assistant","content":[{"type":"text","text":"` + reply + `"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// scratchSession is a parent session whose transcript path exists, so
// SubagentDir resolves somewhere real.
func scratchSession(t *testing.T) session.Session {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	return session.Session{ID: "sess", Transcript: path, CWD: dir}
}

func stop(agentID, transcriptPath string, replyBytes int) event.Event {
	return event.Event{
		TS:      time.Date(2026, 8, 23, 20, 4, 12, 0, time.UTC),
		Event:   "SubagentStop",
		Session: "sess",
		AgentID: agentID,
		Sizes:   map[string]int{"last_assistant_message": replyBytes},
		Detail:  map[string]any{"agent_transcript_path": transcriptPath},
	}
}

// The bug this was written for. A stop event names its own transcript, and that
// path is taken at its word rather than rebuilt from the parent's — so an agent
// whose transcript is not under the session's subagents/ directory is still
// found, typed, and analyzed instead of appearing as an unnamed row.
func TestLoadAgentsFollowsThePathTheEventNames(t *testing.T) {
	s := scratchSession(t)
	elsewhere := filepath.Join(t.TempDir(), "somewhere-else", "agent-a1.jsonl")
	agentTranscript(t, elsewhere, "review the diff", "two findings")

	agents, untraced := loadAgents(s, []event.Event{stop("a1", elsewhere, 38)}, nil)

	if untraced != 0 {
		t.Errorf("untraced = %d, want 0", untraced)
	}
	if len(agents) != 1 {
		t.Fatalf("got %d agents, want 1", len(agents))
	}
	a := agents[0]
	if !a.Analyzed {
		t.Error("the agent was not analyzed, so its transcript was not found")
	}
	if a.Label() != "code-reviewer" {
		t.Errorf("label = %q, want the type off the transcript", a.Label())
	}
	if a.TranscriptPath != elsewhere {
		t.Errorf("TranscriptPath = %q", a.TranscriptPath)
	}
}

// A row for one of these carries eight characters of an id and nothing else,
// which reads as an agent that ran and returned nothing. What is missing is the
// file, so it is counted rather than listed.
func TestLoadAgentsCountsRatherThanListsAnAgentWithNoTranscript(t *testing.T) {
	s := scratchSession(t)
	gone := filepath.Join(t.TempDir(), "agent-a1.jsonl") // never written

	agents, untraced := loadAgents(s, []event.Event{stop("a1", gone, 9)}, nil)

	if len(agents) != 0 {
		t.Errorf("got %d agents, want none listed: %+v", len(agents), agents)
	}
	if untraced != 1 {
		t.Errorf("untraced = %d, want 1", untraced)
	}
}

// A running agent has no transcript yet and is the thing you are waiting on, so
// it stays in the list even though there is nothing to analyze.
func TestLoadAgentsKeepsARunningAgentWithNoTranscript(t *testing.T) {
	s := scratchSession(t)
	start := event.Event{
		TS: time.Date(2026, 8, 23, 20, 3, 12, 0, time.UTC), Event: "SubagentStart",
		Session: "sess", AgentID: "a1", AgentType: "code-reviewer",
	}

	agents, untraced := loadAgents(s, []event.Event{start}, nil)

	if untraced != 0 {
		t.Errorf("untraced = %d, want 0 — a running agent is not a missing one", untraced)
	}
	if len(agents) != 1 || !agents[0].Running {
		t.Fatalf("got %+v, want one running agent", agents)
	}
}

// The hook's last_assistant_message was measured carrying the *parent*
// conversation's last message. The agent's own transcript is the only thing that
// cannot be wrong about what the agent said.
func TestLoadAgentsSizesTheReplyFromTheTranscriptNotTheEvent(t *testing.T) {
	s := scratchSession(t)
	dir := session.SubagentDir(s.Transcript)
	path := filepath.Join(dir, "agent-a1.jsonl")
	agentTranscript(t, path, "review the diff", "two findings")

	// 38 is the size of "go ahead, make the changes and restack", the parent
	// turn this hook field was found reporting.
	agents, _ := loadAgents(s, []event.Event{stop("a1", path, 38)}, nil)

	if len(agents) != 1 {
		t.Fatalf("got %d agents", len(agents))
	}
	if got, want := agents[0].ReplySize, len("two findings"); got != want {
		t.Errorf("ReplySize = %d, want %d — the agent's own final message", got, want)
	}
}

// SubagentStart never fires, so without this the elapsed column can never fill.
// The transcript's own first and last timestamps are real data.
func TestLoadAgentsTakesElapsedFromTheTranscriptWhenNoStartFired(t *testing.T) {
	s := scratchSession(t)
	path := filepath.Join(session.SubagentDir(s.Transcript), "agent-a1.jsonl")
	agentTranscript(t, path, "review the diff", "two findings")

	agents, _ := loadAgents(s, []event.Event{stop("a1", path, 38)}, nil)

	if len(agents) != 1 {
		t.Fatalf("got %d agents", len(agents))
	}
	d, ok := agents[0].Elapsed()
	if !ok {
		t.Fatal("Elapsed reported nothing, so the column stays empty")
	}
	if d != time.Minute {
		t.Errorf("Elapsed = %s, want 1m0s", d)
	}
}

// Two sources for one agent must not become two rows. This is what produced a
// list of roughly double the agents that actually ran.
func TestLoadAgentsDoesNotDoubleCountOneAgent(t *testing.T) {
	s := scratchSession(t)
	// Present in the session's own directory *and* named by the event, which is
	// the ordinary case.
	path := filepath.Join(session.SubagentDir(s.Transcript), "agent-a1.jsonl")
	agentTranscript(t, path, "review the diff", "two findings")

	agents, untraced := loadAgents(s, []event.Event{stop("a1", path, 38)}, nil)

	if len(agents) != 1 || untraced != 0 {
		t.Fatalf("got %d agents and %d untraced, want exactly one agent", len(agents), untraced)
	}
}

// A transcript with no events at all still belongs in the list: it is a subagent
// that ran before the hooks were installed.
func TestLoadAgentsKeepsATranscriptWithNoEvents(t *testing.T) {
	s := scratchSession(t)
	path := filepath.Join(session.SubagentDir(s.Transcript), "agent-a1.jsonl")
	agentTranscript(t, path, "review the diff", "two findings")

	agents, untraced := loadAgents(s, nil, nil)

	if len(agents) != 1 || untraced != 0 {
		t.Fatalf("got %d agents, %d untraced", len(agents), untraced)
	}
	if !agents[0].Analyzed {
		t.Error("a transcript with no events was not analyzed")
	}
}

// Events from another session are not this session's agents.
func TestLoadAgentsIgnoresAnotherSessionsEvents(t *testing.T) {
	s := scratchSession(t)
	other := stop("a1", filepath.Join(t.TempDir(), "agent-a1.jsonl"), 9)
	other.Session = "somebody-else"

	agents, untraced := loadAgents(s, []event.Event{other}, nil)

	if len(agents) != 0 || untraced != 0 {
		t.Errorf("got %d agents, %d untraced", len(agents), untraced)
	}
}

// The parent's Task tool call is the only description written for a person, and
// it joins on the prompt because nothing else is shared.
func TestLoadAgentsTakesTheTaskFromTheParentsSpawn(t *testing.T) {
	s := scratchSession(t)
	path := filepath.Join(session.SubagentDir(s.Transcript), "agent-a1.jsonl")
	agentTranscript(t, path, "review the diff", "two findings")

	parent := []transcript.Line{{
		Type: "assistant",
		Message: &transcript.Message{
			Role: "assistant",
			Content: []byte(`[{"type":"tool_use","id":"t1","name":"Agent","input":` +
				`{"subagent_type":"code-reviewer","description":"Review the diff",` +
				`"prompt":"review the diff"}}]`),
		},
	}}

	agents, _ := loadAgents(s, []event.Event{stop("a1", path, 38)}, parent)

	if len(agents) != 1 {
		t.Fatalf("got %d agents", len(agents))
	}
	if agents[0].Task != "Review the diff" {
		t.Errorf("Task = %q, want the parent's description", agents[0].Task)
	}
}
