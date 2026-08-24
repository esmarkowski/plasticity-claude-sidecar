package attrib

import "testing"

// The estimator only has to be right on average — every bucket is reconciled
// against real usage afterwards. What it must get right is the ordering: dense
// JSON has to cost more per character than prose, or the correction factor
// cannot be a single number.
func TestRatioOrdering(t *testing.T) {
	prose := "The quick brown fox jumped over the lazy dog and kept running for a while."
	code := `func main() { fmt.Println("hello") }`
	dense := `{"a":[1,2,3],"b":{"c":"d"},"e":null,"f":true,"g":[{"h":1}]}`

	pr, cr, dr := ratio(prose), ratio(code), ratio(dense)
	if !(pr > cr && cr >= dr) {
		t.Fatalf("expected prose > code >= dense chars-per-token, got %.1f %.1f %.1f", pr, cr, dr)
	}
}

func TestEstimateNonZeroForNonEmpty(t *testing.T) {
	if got := Estimate(""); got != 0 {
		t.Errorf("empty string estimated at %d", got)
	}
	if got := Estimate("x"); got != 1 {
		t.Errorf("single char estimated at %d, want 1", got)
	}
}

// Attachments carry their injected text under a different key for every type,
// and new types appear regularly, so size is measured by summing every string
// rather than by naming fields.
func TestEstimateStringsWalksNestedValues(t *testing.T) {
	v := map[string]any{
		"type":    "skill_listing", // discriminator, not payload
		"content": []any{"first entry", "second entry"},
		"count":   float64(2), // numbers are structure
		"nested":  map[string]any{"text": "deeper"},
	}
	got := EstimateStrings(v)
	want := Estimate("first entry") + Estimate("second entry") + Estimate("deeper")
	if got != want {
		t.Fatalf("EstimateStrings = %d, want %d (type key and numbers must be skipped)", got, want)
	}
}

// A fit needs enough requests to separate the constant from the slope, and it
// must refuse rather than return a wild scale from three data points.
func TestFitRefusesTooFewPoints(t *testing.T) {
	pts := []AuditPoint{{Measured: 10, Context: 100}, {Measured: 20, Context: 200}}
	if _, ok := Fit(pts); ok {
		t.Error("Fit accepted two points")
	}
	if _, ok := FitScale(pts, 0); ok {
		t.Error("FitScale accepted two points")
	}
}

// The fit has to recover a known slope from clean data, otherwise every number
// downstream is scaled by noise.
func TestFitRecoversKnownRelationship(t *testing.T) {
	const (
		base  = 30_000
		scale = 1.35
	)
	var pts []AuditPoint
	for i := 1; i <= 40; i++ {
		measured := i * 1000
		pts = append(pts, AuditPoint{
			Request:  i,
			Measured: measured,
			Thinking: i * 100,
			Context:  base + int(scale*float64(measured)) + i*100,
		})
	}
	got, ok := FitScale(pts, base)
	if !ok {
		t.Fatal("FitScale refused clean data")
	}
	if got < scale-0.02 || got > scale+0.02 {
		t.Fatalf("FitScale = %.3f, want ~%.2f", got, scale)
	}

	c, ok := Fit(pts)
	if !ok {
		t.Fatal("Fit refused clean data")
	}
	if c.R2 < 0.99 {
		t.Errorf("R2 = %.4f on synthetic linear data", c.R2)
	}
	if c.Base < base-1500 || c.Base > base+1500 {
		t.Errorf("Fit base = %d, want ~%d", c.Base, base)
	}
}

// A hook_success repeats its injected text in both `content` and `stdout`.
// Summing every string — the right default for attachments generally — charges
// the hook twice for one injection.
func TestAttachmentTextDoesNotDoubleCountHookOutput(t *testing.T) {
	const injected = "CAVEMAN MODE ACTIVE — respond terse."
	att := map[string]any{
		"type":     "hook_success",
		"hookName": "SessionStart:resume",
		"content":  injected,
		"stdout":   injected,
		"stderr":   "",
		"command":  "Loading caveman mode...",
	}
	if got, want := attachmentText(att, "hook_success"), injected; got != want {
		t.Fatalf("attachmentText = %q, want only the injected content", got)
	}
	// And the row is named for the hook, so the breakdown says which hook.
	if got := attachmentLabel(att, "hook_success"); got != "SessionStart:resume" {
		t.Errorf("attachmentLabel = %q, want the hook name", got)
	}
}

// The generic path must still walk unknown attachment shapes, since new
// attachment types appear faster than this program tracks them.
func TestAttachmentTextFallsBackToWalkingStrings(t *testing.T) {
	att := map[string]any{"type": "brand_new_thing", "payload": "some text"}
	if got := attachmentText(att, "brand_new_thing"); got == "" {
		t.Fatal("unknown attachment type measured as empty")
	}
	if got := attachmentLabel(att, "brand_new_thing"); got != "brand_new_thing" {
		t.Errorf("attachmentLabel = %q", got)
	}
}

// A rule or memory file pulled in mid-session arrives as an attachment carrying
// the whole text. It was falling through to reminders, which put instruction
// files the user wrote in the same bucket as harness notices and left the rules
// tab unable to name them.
func TestNestedMemoryIsARuleAndIsNamedByItsPath(t *testing.T) {
	att := map[string]any{
		"type": "nested_memory",
		"path": "/repo/.claude/rules/models.md",
		"content": map[string]any{
			"path":    "/repo/.claude/rules/models.md",
			"type":    "Project",
			"content": "# Models\n\nUse concerns.\n",
		},
	}
	if got := attachmentBucket["nested_memory"]; got != BucketRules {
		t.Errorf("nested_memory is filed under %q, want rules & memory", got)
	}
	if got := attachmentLabel(att, "nested_memory"); got != "/repo/.claude/rules/models.md" {
		t.Errorf("label = %q, want the file's path", got)
	}
	// The text that reaches the model, not every string in the attachment: the
	// path appears twice and charging a rule for its own name is not measuring.
	got := attachmentText(att, "nested_memory")
	if got != "# Models\n\nUse concerns.\n" {
		t.Errorf("text = %q", got)
	}
	// A shape other than the one seen falls back to everything, since too much
	// beats silently zero.
	odd := map[string]any{"type": "nested_memory", "content": "flat string"}
	if attachmentText(odd, "nested_memory") == "" {
		t.Error("an unfamiliar shape was measured as nothing")
	}
}
