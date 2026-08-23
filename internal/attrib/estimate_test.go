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
