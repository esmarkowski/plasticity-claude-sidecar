package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/esmarkowski/plasticity-claude-sidecar/internal/attrib"
)

// Colors are AdaptiveColor throughout so the dashboard is legible in a light or
// dark Ghostty theme without a config switch.
var (
	fg     = lipgloss.AdaptiveColor{Light: "#1F2430", Dark: "#E4E6EB"}
	dim    = lipgloss.AdaptiveColor{Light: "#7A8194", Dark: "#767C8C"}
	faint  = lipgloss.AdaptiveColor{Light: "#B4BAC8", Dark: "#454A57"}
	accent = lipgloss.AdaptiveColor{Light: "#4A6FE0", Dark: "#7E9CFF"}
	warn   = lipgloss.AdaptiveColor{Light: "#B4681A", Dark: "#E8A94A"}
	bad    = lipgloss.AdaptiveColor{Light: "#B23A34", Dark: "#F0776E"}
	good   = lipgloss.AdaptiveColor{Light: "#2F7D4F", Dark: "#63C68A"}
)

// bucketColor gives every category a stable hue, so the same colour means the
// same thing in the stacked bar, the legend, and every drill-down.
var bucketColor = map[attrib.Bucket]lipgloss.AdaptiveColor{
	attrib.BucketSystem:       {Light: "#6B5FC4", Dark: "#9A8FE8"},
	attrib.BucketRules:        {Light: "#B4801A", Dark: "#E5B44A"},
	attrib.BucketSkills:       {Light: "#1E8A82", Dark: "#4FC9BF"},
	attrib.BucketAgents:       {Light: "#3A72C4", Dark: "#6FA8F0"},
	attrib.BucketToolDeltas:   {Light: "#5A5FC4", Dark: "#8F94EA"},
	attrib.BucketUser:         {Light: "#2F8A52", Dark: "#69CB8C"},
	attrib.BucketAssistant:    {Light: "#2585A8", Dark: "#5FBEDC"},
	attrib.BucketThinking:     {Light: "#9A4FA8", Dark: "#CE85DC"},
	attrib.BucketToolCalls:    {Light: "#C4661F", Dark: "#EFA05A"},
	attrib.BucketToolResults:  {Light: "#B2453C", Dark: "#EE8177"},
	attrib.BucketHookOutput:   {Light: "#8A5A2B", Dark: "#C89A5F"},
	attrib.BucketReminders:    {Light: "#6E7484", Dark: "#9299A8"},
	attrib.BucketUnattributed: {Light: "#9AA0AE", Dark: "#5B6170"},
}

func colorFor(b attrib.Bucket) lipgloss.AdaptiveColor {
	if c, ok := bucketColor[b]; ok {
		return c
	}
	return dim
}

var (
	// Padding is left/right only: vertical padding would waste two rows of a
	// dashboard that is competing for height with the session it describes.
	panel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(faint).
		Padding(0, 1)

	titleStyle = lipgloss.NewStyle().Foreground(fg).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(dim)
	faintStyle = lipgloss.NewStyle().Foreground(faint)
	numStyle   = lipgloss.NewStyle().Foreground(fg)
	warnStyle  = lipgloss.NewStyle().Foreground(warn)
	badStyle   = lipgloss.NewStyle().Foreground(bad)
	goodStyle  = lipgloss.NewStyle().Foreground(good)

	chipOn = lipgloss.NewStyle().Foreground(lipgloss.Color("#0B0D12")).
		Background(accent).Bold(true).Padding(0, 1)
	chipOff = lipgloss.NewStyle().Foreground(dim).Padding(0, 1)

	accentStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)

	keyStyle  = lipgloss.NewStyle().Foreground(accent).Bold(true)
	helpStyle = lipgloss.NewStyle().Foreground(faint)
)

// shades derives n tellable-apart variants of a bucket's colour, for breaking
// one row's bar into the parts that make it up.
//
// Variants of the one hue rather than n new hues: the parts of Bash are still
// tool calls, and the bar should still read as the tool-calls colour. It also
// keeps the promise bucketColor makes — a hue means one thing everywhere.
func shades(b attrib.Bucket, n int) []lipgloss.AdaptiveColor {
	base := colorFor(b)
	if n < 1 {
		n = 1
	}
	out := make([]lipgloss.AdaptiveColor, 0, n)
	for i := range n {
		// The ramp stops well short of the background, and short of where it used
		// to: a nested bar fades again on top of this, and the two compounding ran
		// the last part of a long breakdown into the background entirely.
		f := 0.0
		if n > 1 {
			f = 0.45 * float64(i) / float64(n-1)
		}
		out = append(out, fade(base, f))
	}
	return out
}

// nestedFade is how far a nested row's bar is pushed toward the background.
//
// The bar on the parent row is the one making the point; the ones under it are
// its parts, and at full strength a breakdown reads as six competing bars rather
// than as the inside of one.
const nestedFade = 0.4

// fade mixes a colour toward the background — the terminal's version of turning
// the opacity down. Toward white under a light theme and black under a dark one,
// which is what makes it read as faded either way rather than as a different
// colour under one of them.
func fade(c lipgloss.AdaptiveColor, f float64) lipgloss.AdaptiveColor {
	if f <= 0 {
		return c
	}
	return lipgloss.AdaptiveColor{
		Light: blend(c.Light, "#FFFFFF", f),
		Dark:  blend(c.Dark, "#000000", f),
	}
}

// blend mixes one hex colour toward another. Unparseable input returns the base
// unchanged: a flat colour is a worse bar, but a panic in the render loop takes
// the whole dashboard with it.
func blend(hex, toward string, f float64) string {
	from, ok := parseHex(hex)
	to, ok2 := parseHex(toward)
	if !ok || !ok2 {
		return hex
	}
	var mixed [3]int
	for i := range mixed {
		mixed[i] = from[i] + int(float64(to[i]-from[i])*f)
	}
	return fmt.Sprintf("#%02X%02X%02X", mixed[0], mixed[1], mixed[2])
}

func parseHex(hex string) ([3]int, bool) {
	var out [3]int
	h := strings.TrimPrefix(hex, "#")
	if len(h) != 6 {
		return out, false
	}
	for i := range out {
		v, err := strconv.ParseUint(h[i*2:i*2+2], 16, 8)
		if err != nil {
			return out, false
		}
		out[i] = int(v)
	}
	return out, true
}
