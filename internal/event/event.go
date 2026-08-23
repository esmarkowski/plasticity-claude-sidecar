// Package event defines the sidecar's on-disk event log: the one thing a Claude
// Code hook writes and the one thing the dashboard reads for live signal.
//
// The log is append-only newline-delimited JSON. That choice is load-bearing:
// a hook's only job is a single O_APPEND write, which the kernel guarantees is
// atomic for a payload this small, so the ~20 hook types firing in parallel
// cannot interleave. Nothing has to be running to receive the write, so a
// crashed or absent dashboard can never stall a turn.
package event

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Event is one hook firing.
//
// The typed fields are the ones the dashboard indexes on. Everything else from
// the hook payload survives in Detail, so a new hook field shows up in the log
// without a schema change here — worth it because the harness adds hook events
// faster than this repo will track them.
type Event struct {
	TS         time.Time         `json:"ts"`
	Event      string            `json:"event"`
	Session    string            `json:"session,omitempty"`
	PromptID   string            `json:"prompt_id,omitempty"`
	AgentID    string            `json:"agent_id,omitempty"`
	AgentType  string            `json:"agent_type,omitempty"`
	CWD        string            `json:"cwd,omitempty"`
	Transcript string            `json:"transcript,omitempty"`
	PermMode   string            `json:"perm_mode,omitempty"`
	Effort     string            `json:"effort,omitempty"`
	Tool       string            `json:"tool,omitempty"`
	ToolUseID  string            `json:"tool_use_id,omitempty"`
	Target     string            `json:"target,omitempty"`
	Sizes      map[string]int    `json:"sizes,omitempty"`
	Previews   map[string]string `json:"previews,omitempty"`
	Detail     map[string]any    `json:"detail,omitempty"`
}

// Dir is where every sidecar file lives. Under ~/.claude so it sits beside the
// transcripts it describes and gets swept up by the same backup habits.
func Dir() string {
	if d := os.Getenv("CLAUDE_SIDECAR_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "claude-sidecar")
	}
	return filepath.Join(home, ".claude", "sidecar")
}

// LogPath is the append-only event log.
func LogPath() string { return filepath.Join(Dir(), "events.jsonl") }

// maxLogBytes caps the log before rotation. The dashboard replays the whole
// file on start, so this is really a bound on startup time.
const maxLogBytes = 64 << 20

// Append writes one event as a single line. Callers in hook context must ignore
// the error and exit 0 regardless: a broken sidecar must never fail a hook.
func Append(ev Event) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	rotate()
	f, err := os.OpenFile(LogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	// One write call, one line. Splitting this into two writes is what would
	// let parallel hooks interleave.
	_, err = f.Write(append(line, '\n'))
	return err
}

func rotate() {
	fi, err := os.Stat(LogPath())
	if err != nil || fi.Size() < maxLogBytes {
		return
	}
	_ = os.Rename(LogPath(), filepath.Join(Dir(), "events.1.jsonl"))
}

// String renders an event for the plain-text log view.
func (e Event) String() string {
	s := fmt.Sprintf("%s  %-22s", e.TS.Format("15:04:05"), e.Event)
	if e.Tool != "" {
		s += "  " + e.Tool
	}
	if e.AgentType != "" {
		s += "  [" + e.AgentType + "]"
	}
	if t := e.label(); t != "" {
		s += "  " + t
	}
	return s
}

// label is the one-line "what was this about" for a log row: the tool's target
// when there is one, otherwise whichever detail field names the thing acted on.
func (e Event) label() string {
	if e.Target != "" {
		return e.Target
	}
	for _, k := range []string{"file_path", "trigger_file_path", "new_cwd", "worktree_path", "command", "message", "config_source"} {
		if s, ok := e.Detail[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
