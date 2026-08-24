package attrib

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/event"
)

// Calibration converts this program's character-density estimates into real
// tokens, and separates out the constant that is never in the transcript.
//
// It is fitted, not tuned. Every request in a session gives one observation of
//
//	context = base + scale × measured + thinking
//
// where context and thinking are exact numbers from the API and measured is our
// estimate of the transcript text feeding that request. base is the system
// prompt plus tool schemas — constant for a session, because the system prompt
// does not change — and scale corrects the chars-per-token guess. Least squares
// over a session's requests recovers both.
//
// This is why the audit view matters: it is the residual plot this fit is
// derived from, so a bad fit is visible rather than hidden.
type Calibration struct {
	Scale  float64 `json:"scale"`
	Base   int     `json:"base"`
	Points int     `json:"points"`
	R2     float64 `json:"r2"`
	Model  string  `json:"model,omitempty"`
}

// minPoints is how many requests are needed before a fit is worth trusting. A
// handful of early requests are dominated by the constant and cannot separate
// it from the slope.
const minPoints = 8

// plausible bounds on the fit. A scale outside this range means the fit picked
// up something other than tokenization — usually a session so short that base
// and slope are not yet separable.
const (
	minScale = 0.6
	maxScale = 3.0
)

// Fit recovers scale and base from a session's own request history.
func Fit(points []AuditPoint) (Calibration, bool) {
	if len(points) < minPoints {
		return Calibration{}, false
	}
	var n, sx, sy, sxx, sxy float64
	for _, p := range points {
		x := float64(p.Measured)
		y := float64(p.Context - p.Thinking)
		n++
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return Calibration{}, false
	}
	scale := (n*sxy - sx*sy) / den
	base := (sy - scale*sx) / n
	if scale < minScale || scale > maxScale || base < 0 {
		return Calibration{}, false
	}

	// Coefficient of determination, so a poor fit can be reported rather than
	// quietly applied.
	mean := sy / n
	var ssTot, ssRes float64
	for _, p := range points {
		y := float64(p.Context - p.Thinking)
		pred := base + scale*float64(p.Measured)
		ssTot += (y - mean) * (y - mean)
		ssRes += (y - pred) * (y - pred)
	}
	r2 := 1.0
	if ssTot > 0 {
		r2 = 1 - ssRes/ssTot
	}
	return Calibration{Scale: scale, Base: int(math.Round(base)), Points: len(points), R2: r2}, true
}

// calibrationPath stores the last good fit, so a fresh session starts with a
// known system-prompt size instead of waiting several requests to infer one.
func calibrationPath() string {
	return filepath.Join(event.Dir(), "calibration.json")
}

// LoadCalibration returns the persisted fit for a model, if there is one.
func LoadCalibration(model string) (Calibration, bool) {
	b, err := os.ReadFile(calibrationPath())
	if err != nil {
		return Calibration{}, false
	}
	var byModel map[string]Calibration
	if err := json.Unmarshal(b, &byModel); err != nil {
		return Calibration{}, false
	}
	c, ok := byModel[model]
	return c, ok
}

// SaveCalibration records a fit, keyed by model. Only better-fitting results
// replace an existing entry, so one odd session cannot poison the baseline.
func SaveCalibration(model string, c Calibration) error {
	if model == "" {
		return nil
	}
	byModel := map[string]Calibration{}
	if b, err := os.ReadFile(calibrationPath()); err == nil {
		_ = json.Unmarshal(b, &byModel)
	}
	if prev, ok := byModel[model]; ok && prev.R2 > c.R2 && prev.Points >= c.Points {
		return nil
	}
	c.Model = model
	byModel[model] = c
	b, err := json.MarshalIndent(byModel, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(event.Dir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(calibrationPath(), append(b, '\n'), 0o600)
}

// FitScale recovers only the chars-per-token correction, with the constant
// already known from a harness probe.
//
// Fitting one parameter instead of two is worth the special case: the probe
// measures the constant directly, and letting least squares re-estimate it
// would trade a measurement for an inference.
func FitScale(points []AuditPoint, base int) (float64, bool) {
	if len(points) < minPoints {
		return 0, false
	}
	var sxx, sxy float64
	for _, p := range points {
		x := float64(p.Measured)
		y := float64(p.Context - p.Thinking - base)
		sxx += x * x
		sxy += x * y
	}
	if sxx == 0 {
		return 0, false
	}
	scale := sxy / sxx
	if scale < minScale || scale > maxScale {
		return 0, false
	}
	return scale, true
}
