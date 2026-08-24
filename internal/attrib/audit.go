package attrib

import (
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/event"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/harness"
	"github.com/esmarkowski/plasticity-claude-sidecar/internal/transcript"
)

// AuditPoint compares what we could measure against what the API actually
// billed, for one request.
type AuditPoint struct {
	Request  int
	Context  int // exact, from usage
	Measured int // estimated from the transcript prefix feeding this request
	Residual int // Context - Measured: system prompt, tool schemas, and whatever we missed
	Thinking int // reasoning tokens generated up to this point
	// Detail names what landed in the window since the previous request, largest
	// first. This is the answer to "why did it jump here", which the columns can
	// only ever measure.
	//
	// Accounts for the jump where the jump is the larger number, with the
	// difference named "unexplained"; where the estimates overshoot it accounts
	// for them instead. Either way the parts add up to the whole they are shown
	// against.
	Detail []Item
}

// Audit reconstructs the residual request by request.
//
// This is the engine's own regression test, and the reason to trust its
// numbers. The system prompt and tool schemas do not change during a session,
// so if attribution is complete the residual must be roughly flat. A residual
// that climbs steadily means some category that accumulates — reasoning,
// re-injected rules, an attachment shape we do not recognize — is being missed
// and silently absorbed into "system + tools".
func Audit(lines []transcript.Line, events []event.Event, snap harness.Snapshot) []AuditPoint {
	return auditChain(transcript.Chain(lines), snap)
}

// auditChain is Audit over an already-walked chain, so callers that have one
// do not pay for the walk twice.
func auditChain(chain []transcript.Line, snap harness.Snapshot) []AuditPoint {
	var out []AuditPoint
	// Measured counts only text whose size this program estimates, so the fit
	// derived from it corrects that estimate and nothing else. Anything with an
	// exact source — the probe's constants, reasoning tokens from usage — is
	// deliberately excluded.
	measured := 0
	thinking := 0
	seen := map[string]bool{}

	// What has landed since the last request, and the names to call it by. The
	// tool map spans the whole walk because a result is credited to the call it
	// answers, and the two are separate lines.
	gap := map[string]*Item{}
	toolByID := map[string]string{}
	lastThinking, lastContext := 0, 0

	for _, l := range chain {
		if l.Message != nil && l.Message.Usage != nil {
			key := l.RequestID
			if key == "" {
				key = l.MessageID
			}
			if key != "" && !seen[key] {
				seen[key] = true
				ctx := l.Message.Usage.ContextTokens()
				if ctx > 0 {
					// Reasoning from the previous request entered the window in
					// this gap like everything else in it.
					if t := thinking - lastThinking; t > 0 {
						credit(gap, "thinking", t)
					}
					// The jump is exact and these are estimates, so the two rarely
					// agree. Whichever is larger is the denominator, and where the
					// jump is larger the difference gets a row of its own — this is
					// the tab that exists to show the residual, and a breakdown
					// quietly explaining a third of a jump while its shares add to
					// a hundred would hide exactly what it is for.
					named, delta := sumOf(gap), ctx-lastContext
					if delta > named {
						credit(gap, "unexplained", delta-named)
					}
					out = append(out, AuditPoint{
						Request:  len(out) + 1,
						Context:  ctx,
						Measured: measured,
						Residual: ctx - measured,
						Thinking: thinking,
						Detail:   rankChildren(gap, maxInt(delta, named)),
					})
					gap = map[string]*Item{}
					lastThinking, lastContext = thinking, ctx
				}
				thinking += l.Message.Usage.Details.ThinkingTokens
			}
		}
		// One walk of the line feeds both the running total and the breakdown, so
		// the two cannot drift apart.
		for _, it := range lineItems(l, snap, toolByID) {
			credit(gap, it.Name, it.Tokens)
			measured += it.Tokens
		}
	}
	return out
}

// credit adds to a running breakdown.
func credit(into map[string]*Item, name string, tokens int) {
	if name == "" || tokens <= 0 {
		return
	}
	it, ok := into[name]
	if !ok {
		it = &Item{Name: name}
		into[name] = it
	}
	it.Tokens += tokens
	it.Count++
}

func sumOf(items map[string]*Item) int {
	n := 0
	for _, it := range items {
		n += it.Tokens
	}
	return n
}

// lineItems is what one transcript line contributes, labelled.
//
// toolByID carries tool names across lines so a result can be credited to the
// call it answers; pass nil when only the total is wanted.
func lineItems(l transcript.Line, snap harness.Snapshot, toolByID map[string]string) []Item {
	if l.Attachment != nil {
		kind, _ := l.Attachment["type"].(string)
		if b, ok := attachmentBucket[kind]; ok && snap.OK() && (b == BucketSkills || b == BucketAgents) {
			// Already in the probe's constants; counting it here too would pull
			// the fitted constant down by the size of the listings.
			return nil
		}
		return []Item{{
			Name:   attachmentLabel(l.Attachment, kind),
			Tokens: Estimate(attachmentText(l.Attachment, kind)),
		}}
	}
	if l.Message == nil {
		return nil
	}
	var out []Item
	for _, b := range l.Message.Blocks() {
		switch b.Type {
		case "text":
			name := "assistant text"
			if l.Message.Role == "user" {
				name = "user message"
			}
			out = append(out, Item{Name: name, Tokens: Estimate(b.Text)})
		case "tool_use":
			label := toolLabel(b.Name, b.Input)
			if toolByID != nil {
				toolByID[b.ID] = label
			}
			out = append(out, Item{Name: label, Tokens: Estimate(string(b.Input))})
		case "tool_result":
			// Named after the call, which is where the file path or the command
			// is: a result on its own is an anonymous wall of text.
			label := toolByID[b.ToolUseID]
			if label == "" {
				label = "tool result"
			}
			out = append(out, Item{Name: label, Tokens: Estimate(resultText(b))})
		}
	}
	return out
}
