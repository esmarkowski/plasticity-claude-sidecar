package ui

import (
	"github.com/charmbracelet/lipgloss"

	"claude-sidecar/internal/attrib"
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

	keyStyle  = lipgloss.NewStyle().Foreground(accent).Bold(true)
	helpStyle = lipgloss.NewStyle().Foreground(faint)
)
