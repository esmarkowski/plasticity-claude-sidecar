package transcript

import "time"

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
