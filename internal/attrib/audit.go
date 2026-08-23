package attrib

import (
	"claude-sidecar/internal/event"
	"claude-sidecar/internal/harness"
	"claude-sidecar/internal/transcript"
)

// AuditPoint compares what we could measure against what the API actually
// billed, for one request.
type AuditPoint struct {
	Request  int
	Context  int // exact, from usage
	Measured int // estimated from the transcript prefix feeding this request
	Residual int // Context - Measured: system prompt, tool schemas, and whatever we missed
	Thinking int // reasoning tokens generated up to this point
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
					out = append(out, AuditPoint{
						Request:  len(out) + 1,
						Context:  ctx,
						Measured: measured,
						Residual: ctx - measured,
						Thinking: thinking,
					})
				}
				thinking += l.Message.Usage.Details.ThinkingTokens
			}
		}
		measured += lineTokens(l, snap)
	}
	return out
}

// lineTokens is the estimated text one transcript line contributes to context.
func lineTokens(l transcript.Line, snap harness.Snapshot) int {
	if l.Attachment != nil {
		kind, _ := l.Attachment["type"].(string)
		if b, ok := attachmentBucket[kind]; ok && snap.OK() && (b == BucketSkills || b == BucketAgents) {
			// Already in the probe's constants; counting it here too would pull
			// the fitted constant down by the size of the listings.
			return 0
		}
		return EstimateStrings(map[string]any(l.Attachment))
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
