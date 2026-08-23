package event

import (
	"encoding/json"
	"strings"
	"time"
)

// PreviewLen bounds how much of a bulky field we keep. Enough to recognize a
// prompt or an error, small enough that a session's worth of events stays in
// the low megabytes.
const PreviewLen = 200

// bulky lists payload fields whose values are unbounded — a tool result can be
// megabytes. We record their size and a preview instead of the value, because
// the log's whole value proposition is that replaying it is cheap.
var bulky = map[string]bool{
	"tool_input":             true,
	"tool_output":            true,
	"tool_response":          true,
	"prompt":                 true,
	"expansion":              true,
	"last_assistant_message": true,
	"message_text":           true,
	"stdout":                 true,
	"stderr":                 true,
	"task_data":              true,
	"changes":                true,
	"request_data":           true,
	"user_response":          true,
	"added_lines":            true,
	"content":                true,
}

// targetKeys are the tool_input fields worth surfacing as a one-line label, in
// priority order. Keeps the Tools view readable without keeping the input.
var targetKeys = []string{"file_path", "command", "pattern", "path", "url", "notebook_path", "prompt", "description"}

// FromHook converts a hook's stdin payload into an Event. It never fails: an
// unparseable payload still yields an event recording that the hook fired,
// because "the hook fired and we could not read it" is itself signal.
func FromHook(raw []byte, now time.Time) Event {
	ev := Event{TS: now}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		ev.Event = "unparseable"
		ev.Sizes = map[string]int{"raw": len(raw)}
		ev.Previews = map[string]string{"raw": preview(string(raw))}
		return ev
	}

	ev.Event = str(m["hook_event_name"])
	ev.Session = str(m["session_id"])
	ev.PromptID = str(m["prompt_id"])
	ev.AgentID = str(m["agent_id"])
	ev.AgentType = str(m["agent_type"])
	ev.CWD = str(m["cwd"])
	ev.Transcript = str(m["transcript_path"])
	ev.PermMode = str(m["permission_mode"])
	ev.Effort = effort(m["effort"])
	ev.Tool = str(m["tool_name"])
	ev.ToolUseID = firstStr(m, "tool_use_id", "toolUseID")
	ev.Target = target(m["tool_input"])

	// Everything not promoted to a typed field lands in Detail, with bulky
	// values swapped for a size and a preview.
	consumed := map[string]bool{
		"hook_event_name": true, "session_id": true, "prompt_id": true,
		"agent_id": true, "agent_type": true, "cwd": true,
		"transcript_path": true, "permission_mode": true, "effort": true,
		"tool_name": true, "tool_use_id": true, "toolUseID": true,
	}
	for k, v := range m {
		if consumed[k] {
			continue
		}
		if bulky[k] {
			s := flatten(v)
			if ev.Sizes == nil {
				ev.Sizes = map[string]int{}
				ev.Previews = map[string]string{}
			}
			ev.Sizes[k] = len(s)
			if p := preview(s); p != "" {
				ev.Previews[k] = p
			}
			continue
		}
		if ev.Detail == nil {
			ev.Detail = map[string]any{}
		}
		ev.Detail[k] = v
	}
	return ev
}

// flatten renders any payload value as the text it would occupy in context.
// Strings pass through; structured values are measured as their JSON, which is
// what actually reaches the model for a tool_input.
func flatten(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func preview(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) <= PreviewLen {
		return s
	}
	return string(r[:PreviewLen]) + "…"
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := str(m[k]); s != "" {
			return s
		}
	}
	return ""
}

// effort arrives as {"level":"high"} on tool events but is a bare string in
// some payloads, so accept both rather than losing it to a type assertion.
func effort(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		return str(t["level"])
	}
	return ""
}

func target(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	for _, k := range targetKeys {
		if s := str(m[k]); s != "" {
			return preview(s)
		}
	}
	return ""
}
