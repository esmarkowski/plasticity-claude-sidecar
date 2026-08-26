package transcript

import (
	"testing"
	"time"
)

// What an agent returned is the last thing the agent said, not the last thing in
// the conversation. A trailing user turn must not be mistaken for a reply.
func TestLastAssistantTextIgnoresATrailingUserTurn(t *testing.T) {
	lines, err := Load(write(t,
		msg("u1", "", "user", "find the bug"),
		msg("a1", "u1", "assistant", "looking"),
		msg("a2", "a1", "assistant", "the guard is inverted"),
		msg("u2", "a2", "user", "go ahead, make the changes and restack"),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := LastAssistantText(lines); got != "the guard is inverted" {
		t.Errorf("LastAssistantText = %q", got)
	}
}

// An assistant turn carrying only a tool call has no text, and reporting an
// empty reply for it would read as an agent that returned nothing.
func TestLastAssistantTextSkipsATextlessTurn(t *testing.T) {
	lines, err := Load(write(t,
		msg("a1", "", "assistant", "here is the answer"),
		`{"type":"assistant","uuid":"a2","parentUuid":"a1","message":{"role":"assistant",`+
			`"content":[{"type":"tool_use","id":"t1","name":"Read","input":{}}]}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := LastAssistantText(lines); got != "here is the answer" {
		t.Errorf("LastAssistantText = %q", got)
	}
}

func TestLastAssistantTextIsEmptyWithoutOne(t *testing.T) {
	lines, err := Load(write(t, msg("u1", "", "user", "hello")))
	if err != nil {
		t.Fatal(err)
	}
	if got := LastAssistantText(lines); got != "" {
		t.Errorf("LastAssistantText = %q, want empty", got)
	}
}

// The span is how long a subagent ran. It is taken from the timestamps in the
// file because SubagentStart never fires, so there is no event pair to measure.
func TestSpanIsTheFirstAndLastTimestamp(t *testing.T) {
	stamped := func(uuid, ts string) string {
		return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":null,"timestamp":"` + ts + `",` +
			`"message":{"role":"assistant","content":[{"type":"text","text":"x"}]}}`
	}
	lines, err := Load(write(t,
		stamped("a1", "2026-08-23T20:03:12Z"),
		// Out of order on purpose: the file is not a guarantee of ordering.
		stamped("a3", "2026-08-23T20:05:42Z"),
		stamped("a2", "2026-08-23T20:04:00Z"),
	))
	if err != nil {
		t.Fatal(err)
	}
	start, end, ok := Span(lines)
	if !ok {
		t.Fatal("Span reported nothing")
	}
	if d := end.Sub(start); d != 2*time.Minute+30*time.Second {
		t.Errorf("span = %s, want 2m30s", d)
	}
}

// A transcript with no timestamps must say so rather than return the zero time,
// which would render as a duration since the epoch.
func TestSpanReportsWhenThereAreNoTimestamps(t *testing.T) {
	lines, err := Load(write(t, msg("a1", "", "assistant", "x")))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := Span(lines); ok {
		t.Error("Span claimed a span from lines with no timestamps")
	}
}
