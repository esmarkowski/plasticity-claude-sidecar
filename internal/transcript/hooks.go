package transcript

import (
	"encoding/json"
	"strings"
	"time"
)

// HookRun is one hook invocation as the transcript recorded it.
//
// This is the only account of hooks that covers hooks other than our own. A
// plugin's failing hook writes nothing to our event log — it never gets far
// enough to run anything — but Claude Code still notes the failure here, which
// is how a silently broken hook becomes visible.
type HookRun struct {
	Name       string
	Event      string
	Command    string
	ExitCode   int
	DurationMS int
	Stderr     string
	// Injected is the text the hook contributed to the model's context. Hooks
	// are allowed to add context, and a hook that adds a page of instructions on
	// every session start is a cost worth seeing attributed to it.
	Injected string
	TS       time.Time
	Failed   bool
}

// Hooks extracts every hook invocation the transcript knows about.
//
// Three sources, because each knows something the others do not:
//
//   - hook_non_blocking_error attachments: failures, with exit code and stderr.
//   - hook_success attachments: successes, and the text they injected.
//   - stop_hook_summary system lines: the real command paths and durations for
//     hooks that ran to completion without saying anything.
func Hooks(lines []Line) []HookRun {
	var out []HookRun
	for _, l := range lines {
		if l.Attachment != nil {
			kind, _ := l.Attachment["type"].(string)
			switch kind {
			case "hook_non_blocking_error", "hook_success":
				out = append(out, hookFromAttachment(l))
			}
			continue
		}
		if l.Type == "system" && l.Subtype == "stop_hook_summary" {
			for _, info := range l.HookInfos {
				out = append(out, HookRun{
					Event:      "Stop",
					Command:    info.Command,
					DurationMS: info.DurationMS,
					TS:         l.Timestamp,
				})
			}
		}
	}
	return out
}

func hookFromAttachment(l Line) HookRun {
	a := l.Attachment
	h := HookRun{
		Name:     str(a["hookName"]),
		Event:    str(a["hookEvent"]),
		Command:  str(a["command"]),
		Stderr:   str(a["stderr"]),
		Injected: str(a["content"]),
		TS:       l.Timestamp,
	}
	if v, ok := a["exitCode"].(float64); ok {
		h.ExitCode = int(v)
	}
	if v, ok := a["durationMs"].(float64); ok {
		h.DurationMS = int(v)
	}
	kind, _ := a["type"].(string)
	h.Failed = kind == "hook_non_blocking_error" || h.ExitCode != 0
	return h
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// Spawn is a subagent launch as recorded in the parent's transcript.
//
// The parent is the only place a subagent's human-readable task appears: the
// Agent tool call carries the type and a description written for a person. The
// subagent's own transcript knows its type but only ever sees the prompt.
type Spawn struct {
	Type        string
	Description string
	Prompt      string
}

// Spawns extracts subagent launches, keyed by the prompt each one was given.
//
// Keyed by prompt because there is no shared identifier: the parent records a
// tool_use id, the subagent transcript records an agentId, and nothing joins
// them. The prompt is passed through verbatim as the subagent's first message,
// which makes it the one thing both sides hold.
func Spawns(lines []Line) map[string]Spawn {
	out := map[string]Spawn{}
	for _, l := range lines {
		if l.Message == nil {
			continue
		}
		for _, b := range l.Message.Blocks() {
			if b.Type != "tool_use" || (b.Name != "Agent" && b.Name != "Task") {
				continue
			}
			var in struct {
				SubagentType string `json:"subagent_type"`
				Description  string `json:"description"`
				Prompt       string `json:"prompt"`
			}
			if err := json.Unmarshal(b.Input, &in); err != nil || in.Prompt == "" {
				continue
			}
			out[PromptKey(in.Prompt)] = Spawn{
				Type:        in.SubagentType,
				Description: in.Description,
				Prompt:      in.Prompt,
			}
		}
	}
	return out
}

// PromptKey normalizes a prompt into a join key. A prefix, because the harness
// may append to the prompt it hands the subagent.
func PromptKey(prompt string) string {
	const n = 160
	k := strings.Join(strings.Fields(prompt), " ")
	if len(k) > n {
		k = k[:n]
	}
	return k
}

// FirstPrompt is the task a subagent was given: the first user message of its
// own transcript.
func FirstPrompt(lines []Line) string {
	for _, l := range lines {
		if l.Message == nil || l.Message.Role != "user" {
			continue
		}
		for _, b := range l.Message.Blocks() {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				return b.Text
			}
		}
	}
	return ""
}

// AgentType reads the agent type off a subagent's own transcript.
func AgentType(lines []Line) string {
	for _, l := range lines {
		if l.AttributionAgent != "" {
			return l.AttributionAgent
		}
	}
	return ""
}

// Title is a human-readable name for a session.
//
// Sessions are not anonymous, though a uuid is all they look like from the
// filesystem: a name given on launch is recorded as agentName, and the harness
// generates a summary title of its own. Either beats eight hex characters.
// The last one wins, since both can be revised mid-session.
func Title(lines []Line) string {
	named, generated := "", ""
	for _, l := range lines {
		if l.AgentName != "" {
			named = l.AgentName
		}
		if l.AITitle != "" {
			generated = l.AITitle
		}
	}
	// A name the user chose outranks one the harness inferred.
	if named != "" {
		return named
	}
	return generated
}
