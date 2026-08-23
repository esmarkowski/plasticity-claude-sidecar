package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// write builds a transcript file from raw JSONL lines.
func write(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	var body string
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func msg(uuid, parent, role, text string) string {
	p := "null"
	if parent != "" {
		b, _ := json.Marshal(parent)
		p = string(b)
	}
	return `{"type":"` + role + `","uuid":"` + uuid + `","parentUuid":` + p +
		`,"message":{"role":"` + role + `","content":[{"type":"text","text":"` + text + `"}]}}`
}

func uuids(lines []Line) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.UUID)
	}
	return out
}

// A rewind leaves the abandoned branch in the file forever. Counting it would
// inflate every number in the dashboard, so this is the single most important
// behaviour in the package.
func TestChainExcludesRewoundBranch(t *testing.T) {
	path := write(t,
		msg("a", "", "user", "first"),
		msg("b", "a", "assistant", "reply"),
		// Abandoned: the user rewound to a and asked something else.
		msg("dead1", "b", "user", "abandoned"),
		msg("dead2", "dead1", "assistant", "abandoned reply"),
		// The live branch forks from a.
		msg("c", "a", "user", "second"),
		msg("d", "c", "assistant", "live reply"),
	)
	lines, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := uuids(Chain(lines))
	want := []string{"a", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("chain = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chain = %v, want %v", got, want)
		}
	}
}

// After compaction the model can no longer see anything before the boundary.
// Claude Code encodes that by giving the boundary a nil parentUuid, so walking
// parents backwards stops there — but only if nothing "helpfully" follows
// logicalParentUuid across the gap.
func TestChainStopsAtCompactBoundary(t *testing.T) {
	path := write(t,
		msg("old1", "", "user", "ancient"),
		msg("old2", "old1", "assistant", "ancient reply"),
		`{"type":"system","subtype":"compact_boundary","uuid":"cb","parentUuid":null,`+
			`"logicalParentUuid":"old2","compactMetadata":{"trigger":"auto","preTokens":933612}}`,
		msg("new1", "cb", "user", "after compaction"),
		msg("new2", "new1", "assistant", "reply"),
	)
	lines, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	chain := Chain(lines)
	for _, l := range chain {
		if l.UUID == "old1" || l.UUID == "old2" {
			t.Fatalf("pre-compaction line %q is still counted as in context", l.UUID)
		}
	}
	var boundary *Line
	for i := range chain {
		if chain[i].Subtype == "compact_boundary" {
			boundary = &chain[i]
		}
	}
	if boundary == nil {
		t.Fatal("compact boundary missing from the chain")
	}
	if boundary.Compact == nil || boundary.Compact.PreTokens != 933612 {
		t.Fatalf("compaction metadata not parsed: %+v", boundary.Compact)
	}
}

// Usage is repeated on every content block of one API response. Summing lines
// instead of requests multiplies the total by the reply's block count, which is
// the kind of error that looks plausible and is wrong by 4x.
func TestRequestsDeduplicatesByRequestID(t *testing.T) {
	line := func(uuid, parent, req string) string {
		p := "null"
		if parent != "" {
			p = `"` + parent + `"`
		}
		return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":` + p +
			`,"requestId":"` + req + `","message":{"role":"assistant","usage":` +
			`{"input_tokens":2,"cache_read_input_tokens":1000,"cache_creation_input_tokens":0,` +
			`"output_tokens":50,"output_tokens_details":{"thinking_tokens":10}},` +
			`"content":[{"type":"text","text":"x"}]}}`
	}
	path := write(t,
		line("a", "", "req1"),
		line("b", "a", "req1"),
		line("c", "b", "req1"),
		line("d", "c", "req2"),
	)
	lines, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(Requests(Chain(lines))); got != 2 {
		t.Fatalf("Requests returned %d entries, want 2 — usage is being counted per block", got)
	}
}

// The newest line is routinely half-written while Claude Code flushes. Refusing
// to parse would blank the dashboard exactly when it is being watched.
func TestLoadSkipsTruncatedFinalLine(t *testing.T) {
	path := write(t,
		msg("a", "", "user", "complete"),
		`{"type":"assistant","uuid":"b","parentUuid":"a","message":{"role":"assist`,
	)
	lines, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 (the truncated line should be skipped)", len(lines))
	}
}

// A tool result's content is a string sometimes and an array of blocks other
// times. Both shapes appear in a single real session.
func TestMessageBlocksHandlesBothContentShapes(t *testing.T) {
	var m Message
	if err := json.Unmarshal([]byte(`{"role":"user","content":"plain prompt"}`), &m); err != nil {
		t.Fatal(err)
	}
	blocks := m.Blocks()
	if len(blocks) != 1 || blocks[0].Text != "plain prompt" {
		t.Fatalf("string content became %+v", blocks)
	}

	var m2 Message
	if err := json.Unmarshal([]byte(`{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash"}]}`), &m2); err != nil {
		t.Fatal(err)
	}
	blocks = m2.Blocks()
	if len(blocks) != 1 || blocks[0].ID != "t1" || blocks[0].Name != "Bash" {
		t.Fatalf("array content became %+v", blocks)
	}
}
