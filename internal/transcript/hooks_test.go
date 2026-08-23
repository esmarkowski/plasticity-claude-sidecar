package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

// Verbatim records from a real session on this machine: two hooks failing with
// exit 127 because they invoke `node`, which is not on the PATH a hook gets.
const hookLines = `{"type":"attachment","uuid":"a","parentUuid":null,"attachment":{"type":"hook_non_blocking_error","hookName":"SessionStart:startup","hookEvent":"SessionStart","stderr":"Failed with non-blocking status code: /bin/sh: node: command not found","stdout":"","exitCode":127,"command":"Loading caveman mode...","durationMs":12}}
{"type":"attachment","uuid":"b","parentUuid":"a","attachment":{"type":"hook_success","hookName":"SessionStart:resume","hookEvent":"SessionStart","content":"INJECTED INSTRUCTIONS","stdout":"INJECTED INSTRUCTIONS","stderr":"","exitCode":0,"command":"Loading caveman mode...","durationMs":104}}
{"type":"system","subtype":"stop_hook_summary","uuid":"c","parentUuid":"b","hookCount":2,"hookInfos":[{"command":"$HOME/.claude/hooks/audit-goal.sh","durationMs":26},{"command":"/Users/spencer/.local/bin/sidecar emit","durationMs":17}],"hookErrors":[]}
`

func TestHooksExtractsFailuresSuccessesAndTimings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(hookLines), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	hooks := Hooks(lines)
	if len(hooks) != 4 {
		t.Fatalf("got %d hook runs, want 4 (1 failure, 1 success, 2 from the stop summary)", len(hooks))
	}

	fail := hooks[0]
	if !fail.Failed || fail.ExitCode != 127 {
		t.Errorf("failure not detected: %+v", fail)
	}
	if fail.Name != "SessionStart:startup" || fail.DurationMS != 12 {
		t.Errorf("failure fields wrong: %+v", fail)
	}

	ok := hooks[1]
	if ok.Failed {
		t.Errorf("hook_success marked as failed: %+v", ok)
	}
	// The injected text is what this hook costs the context window on every fire.
	if ok.Injected != "INJECTED INSTRUCTIONS" {
		t.Errorf("injected text = %q", ok.Injected)
	}

	// The stop summary is the only place a hook's real command path appears.
	if hooks[3].Command != "/Users/spencer/.local/bin/sidecar emit" || hooks[3].DurationMS != 17 {
		t.Errorf("stop summary entry wrong: %+v", hooks[3])
	}
}

// A zero exit code with the error attachment type is still a failure; and a
// non-zero code on a "success" attachment is too. Trusting only the type would
// miss the second.
func TestHooksTreatsNonZeroExitAsFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"attachment","uuid":"a","parentUuid":null,"attachment":{"type":"hook_success","hookName":"X","exitCode":2}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, _ := Load(path)
	hooks := Hooks(lines)
	if len(hooks) != 1 || !hooks[0].Failed {
		t.Fatalf("non-zero exit on a success attachment was not flagged: %+v", hooks)
	}
}

// A subagent's type is on its own transcript as attributionAgent, not on the
// SubagentStop hook payload — which is documented to carry agent_type and in
// practice does not.
func TestAgentTypeAndFirstPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.jsonl")
	body := `{"type":"user","uuid":"a","parentUuid":null,"isSidechain":true,"agentId":"a0b7f38e5","message":{"role":"user","content":[{"type":"text","text":"Review the working-tree diff. Be thorough."}]}}
{"type":"assistant","uuid":"b","parentUuid":"a","isSidechain":true,"agentId":"a0b7f38e5","attributionAgent":"review-agent","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := AgentType(lines); got != "review-agent" {
		t.Errorf("AgentType = %q, want review-agent", got)
	}
	if got := FirstPrompt(lines); got != "Review the working-tree diff. Be thorough." {
		t.Errorf("FirstPrompt = %q", got)
	}
}

// The parent's Agent tool call is the only place a task description written for
// a person appears. Nothing joins it to the subagent but the prompt.
func TestSpawnsJoinByPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parent.jsonl")
	body := `{"type":"assistant","uuid":"a","parentUuid":null,"message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Agent","input":{"subagent_type":"Explore","description":"Map domain models","prompt":"Find every model and concern in app/models"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	spawns := Spawns(lines)
	sp, ok := spawns[PromptKey("Find every model and concern in app/models")]
	if !ok {
		t.Fatalf("spawn not found by prompt key; keys were %v", spawns)
	}
	if sp.Type != "Explore" || sp.Description != "Map domain models" {
		t.Errorf("spawn = %+v", sp)
	}

	// Whitespace differences must not break the join, since the two sides are
	// written by different parts of the harness.
	if PromptKey("Find every  model\nand concern in app/models") != PromptKey("Find every model and concern in app/models") {
		t.Error("PromptKey is sensitive to whitespace")
	}
}

// A named session outranks a generated title, and the last value wins since
// both can be revised mid-session.
func TestTitlePrefersGivenName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"ai-title","aiTitle":"Some generated summary","sessionId":"s"}
{"type":"agent-name","agentName":"archspec","sessionId":"s"}
{"type":"ai-title","aiTitle":"A revised summary","sessionId":"s"}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, _ := Load(path)
	if got := Title(lines); got != "archspec" {
		t.Errorf("Title = %q, want the given name", got)
	}

	path2 := filepath.Join(t.TempDir(), "s2.jsonl")
	if err := os.WriteFile(path2, []byte(`{"type":"ai-title","aiTitle":"First"}`+"\n"+`{"type":"ai-title","aiTitle":"Latest"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines2, _ := Load(path2)
	if got := Title(lines2); got != "Latest" {
		t.Errorf("Title = %q, want the most recent generated title", got)
	}
}
