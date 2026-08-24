package attrib

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/harness"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/transcript"
)

// lineTokensOld is the counter lineItems replaced, kept here as the oracle. The
// fitted scale and every bucket derive from this number, so the labelled version
// has to agree with it on every line shape, not just on the ones a fixture
// happens to contain.
func lineTokensOld(l transcript.Line, snap harness.Snapshot) int {
	if l.Attachment != nil {
		kind, _ := l.Attachment["type"].(string)
		if b, ok := attachmentBucket[kind]; ok && snap.OK() && (b == BucketSkills || b == BucketAgents) {
			return 0
		}
		return Estimate(attachmentText(l.Attachment, kind))
	}
	if l.Message == nil {
		return 0
	}
	n := 0
	for _, b := range l.Message.Blocks() {
		switch b.Type {
		case "text":
			n += Estimate(b.Text)
		case "tool_use":
			n += Estimate(string(b.Input))
		case "tool_result":
			n += Estimate(resultText(b))
		}
	}
	return n
}

func TestLineItemsSumsToWhatItReplaced(t *testing.T) {
	body := `{"type":"user","uuid":"a","parentUuid":null,"message":{"role":"user","content":[{"type":"text","text":"do the thing please"}]}}
{"type":"assistant","uuid":"b","parentUuid":"a","requestId":"r1","message":{"role":"assistant","content":[{"type":"text","text":"on it"},{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/app/models/user.rb"}}],"usage":{"input_tokens":10,"cache_read_input_tokens":900,"output_tokens":5,"output_tokens_details":{"reasoning_tokens":40}}}}
{"type":"user","uuid":"c","parentUuid":"b","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"class User < ApplicationRecord\nend"}]}}
{"type":"assistant","uuid":"d","parentUuid":"c","requestId":"r2","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"mise exec -- bundle exec rspec"}}],"usage":{"input_tokens":10,"cache_read_input_tokens":1400,"output_tokens":5,"output_tokens_details":{"reasoning_tokens":12}}}}
{"type":"attachment","uuid":"e","parentUuid":"d","attachment":{"type":"hook_success","hookName":"Stop","content":"INJECTED"}}
{"type":"assistant","uuid":"f","parentUuid":"e","requestId":"r3","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":10,"cache_read_input_tokens":2000,"output_tokens":5}}}
`
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := transcript.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	var snap harness.Snapshot
	for _, l := range transcript.Chain(lines) {
		want := lineTokensOld(l, snap)
		got := 0
		for _, it := range lineItems(l, snap, map[string]string{}) {
			got += it.Tokens
		}
		if got != want {
			t.Errorf("line %s: labelled sum %d, oracle %d", l.UUID, got, want)
		}
	}
}

// The point of the breakdown: the row says the window grew, and this says what
// grew it.
func TestAuditNamesWhatGrewTheWindow(t *testing.T) {
	body := `{"type":"user","uuid":"a","parentUuid":null,"message":{"role":"user","content":[{"type":"text","text":"go"}]}}
{"type":"assistant","uuid":"b","parentUuid":"a","requestId":"r1","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/app/models/user.rb"}}],"usage":{"input_tokens":10,"cache_read_input_tokens":90,"output_tokens":5}}}
{"type":"user","uuid":"c","parentUuid":"b","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"` + longText(4000) + `"}]}}
{"type":"assistant","uuid":"d","parentUuid":"c","requestId":"r2","message":{"role":"assistant","content":[{"type":"text","text":"read it"}],"usage":{"input_tokens":10,"cache_read_input_tokens":1400,"output_tokens":5}}}
`
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, _ := transcript.Load(path)
	points := Audit(lines, nil, harness.Snapshot{})
	if len(points) != 2 {
		t.Fatalf("got %d requests, want 2", len(points))
	}

	// The second request's jump is the file that came back, named after the call
	// that asked for it — "Read" alone would not answer the question.
	if top := points[1].Detail[0]; top.Name != "Read user.rb" {
		t.Errorf("the jump is attributed to %q, want the Read that caused it: %+v",
			top.Name, points[1].Detail)
	}

	// And the parts add up to the jump they are shown against.
	sum, delta := 0, points[1].Context-points[0].Context
	for _, it := range points[1].Detail {
		sum += it.Tokens
	}
	if sum < delta {
		t.Errorf("the breakdown accounts for %d of a %d jump", sum, delta)
	}
}

func longText(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = 'x'
	}
	return string(out)
}
