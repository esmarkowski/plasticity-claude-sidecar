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
