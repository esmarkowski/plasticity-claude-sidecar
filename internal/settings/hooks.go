package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Events are the hook events the sidecar registers for.
//
// Deliberately absent: MessageDisplay, which fires per streamed chunk under a
// 10-second budget and would swamp the log with nothing the transcript does not
// already say.
var Events = []string{
	"SessionStart",
	"SessionEnd",
	"UserPromptSubmit",
	"UserPromptExpansion",
	"Stop",
	"StopFailure",
	"PreToolUse",
	"PostToolUse",
	"PostToolUseFailure",
	"PostToolBatch",
	"SubagentStart",
	"SubagentStop",
	"InstructionsLoaded",
	"PreCompact",
	"PostCompact",
	"ConfigChange",
	"PermissionRequest",
	"PermissionDenied",
	"CwdChanged",
	"Notification",
}

// Path is the user-level settings file. Registering globally rather than
// per-project is the point: the dashboard should light up for whichever session
// you happen to start, without per-repo setup.
func Path() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "settings.json"
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// Register adds a command hook for each event in events, skipping any event
// that already points at this command so the operation is idempotent.
//
// The command must be an absolute path. Hooks run under /bin/sh with none of
// the user's shell activation, so a bare name resolves against a minimal PATH —
// which is exactly how the node-based hooks on this machine came to fail with
// exit 127.
func Register(path, command string, events []string) (added []string, err error) {
	if !filepath.IsAbs(strings.Fields(command)[0]) {
		return nil, fmt.Errorf("hook command must be an absolute path, got %q", command)
	}

	original, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	root, err := ParseObject(original)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	hooksRaw, _ := root.Get("hooks")
	hooks, err := ParseObject(hooksRaw)
	if err != nil {
		return nil, fmt.Errorf("parsing the hooks block of %s: %w", path, err)
	}

	group, err := json.Marshal(map[string]any{
		"hooks": []map[string]string{{"type": "command", "command": command}},
	})
	if err != nil {
		return nil, err
	}

	for _, ev := range events {
		var groups []json.RawMessage
		if raw, ok := hooks.Get(ev); ok {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return nil, fmt.Errorf("parsing hooks.%s of %s: %w", ev, path, err)
			}
		}
		if registered(groups, command) {
			continue
		}
		groups = append(groups, group)
		raw, err := json.Marshal(groups)
		if err != nil {
			return nil, err
		}
		hooks.Set(ev, raw)
		added = append(added, ev)
	}
	if len(added) == 0 {
		return nil, nil
	}

	root.Set("hooks", hooks.Render("  ", 1))

	if len(original) > 0 {
		if err := backup(path, original); err != nil {
			return nil, err
		}
	}
	return added, writeAtomic(path, append(root.Render("  ", 0), '\n'))
}

// registered reports whether any existing group already runs this command.
// Compared against the marshalled JSON so a group of any shape is covered.
func registered(groups []json.RawMessage, command string) bool {
	needle, err := json.Marshal(command)
	if err != nil {
		return false
	}
	for _, g := range groups {
		if strings.Contains(string(g), string(needle)) {
			return true
		}
	}
	return false
}

// backup copies the pre-edit file into ~/.claude/backups, which Claude Code
// already maintains, so a bad edit is always one `cp` from undone.
func backup(path string, original []byte) error {
	dir := filepath.Join(filepath.Dir(path), "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("%s.%s", filepath.Base(path), time.Now().UTC().Format("20060102-150405"))
	return os.WriteFile(filepath.Join(dir, name), original, 0o600)
}

// writeAtomic renames into place so a crash mid-write cannot leave Claude Code
// with a truncated settings file it refuses to start with.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	// Best effort: on the success path the file has already been renamed away.
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
