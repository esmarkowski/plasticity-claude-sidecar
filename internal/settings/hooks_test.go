package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A hand-maintained settings.json read daily by its owner. Reordering its keys
// is a real, if benign, act of vandalism, so it is pinned by a test.
const existing = `{
  "statusLine": {
    "type": "command",
    "command": "bash ~/.claude/scripts/statusline.sh"
  },
  "theme": "auto",
  "model": "opus[1m]",
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [
          {
            "type": "command",
            "command": "$HOME/.claude/hooks/rubocop.sh"
          }
        ]
      }
    ]
  }
}
`

func writeSettings(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func keyOrder(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	o, err := ParseObject(b)
	if err != nil {
		t.Fatal(err)
	}
	return o.Keys()
}

func TestRegisterPreservesKeyOrderAndExistingHooks(t *testing.T) {
	path := writeSettings(t, existing)
	before := keyOrder(t, path)

	added, err := Register(path, "/abs/sidecar emit", []string{"SessionStart", "PostToolUse"})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 2 {
		t.Fatalf("added %v, want both events", added)
	}

	after := keyOrder(t, path)
	if strings.Join(before, ",") != strings.Join(after, ",") {
		t.Fatalf("top-level key order changed: %v -> %v", before, after)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Theme string `json:"theme"`
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, b)
	}
	if parsed.Theme != "auto" {
		t.Errorf("unrelated key was altered: theme = %q", parsed.Theme)
	}

	// The pre-existing rubocop hook must survive alongside the new one.
	post := parsed.Hooks["PostToolUse"]
	if len(post) != 2 {
		t.Fatalf("PostToolUse has %d groups, want the original plus ours", len(post))
	}
	if post[0].Matcher != "Edit|Write" || !strings.Contains(post[0].Hooks[0].Command, "rubocop") {
		t.Errorf("existing hook group was disturbed: %+v", post[0])
	}
}

// install is expected to be run more than once — after an upgrade, or just
// because. A second run must be a no-op rather than adding a duplicate hook
// that then fires twice per event.
func TestRegisterIsIdempotent(t *testing.T) {
	path := writeSettings(t, existing)
	events := []string{"SessionStart", "PostToolUse"}

	if _, err := Register(path, "/abs/sidecar emit", events); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	added, err := Register(path, "/abs/sidecar emit", events)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Errorf("second run added %v", added)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("second run rewrote the file")
	}
}

// Hooks run under /bin/sh with none of the user's shell activation. A relative
// command resolves against a minimal PATH and fails with exit 127 — which is
// exactly how the node-based hooks on this machine were failing, invisibly.
func TestRegisterRejectsRelativeCommand(t *testing.T) {
	path := writeSettings(t, existing)
	if _, err := Register(path, "sidecar emit", []string{"SessionStart"}); err == nil {
		t.Fatal("accepted a relative hook command")
	}
}

func TestRegisterCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	added, err := Register(path, "/abs/sidecar emit", []string{"SessionStart"})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 {
		t.Fatalf("added %v", added)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("wrote invalid JSON: %v\n%s", err, b)
	}
}
