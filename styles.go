package main

import (
	"hash/fnv"

	"github.com/charmbracelet/lipgloss"
)

var (
	accent = lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#9E8CFC"}
	subtle = lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"}
	dim    = lipgloss.AdaptiveColor{Light: "#6E6E6E", Dark: "#8A8A8A"}
	warn   = lipgloss.AdaptiveColor{Light: "#C0392B", Dark: "#E06C75"}
	good   = lipgloss.AdaptiveColor{Light: "#1E824C", Dark: "#98C379"}

	logoStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(accent).
			Padding(0, 1)

	titleStyle  = lipgloss.NewStyle().Bold(true)
	subtleStyle = lipgloss.NewStyle().Foreground(subtle)
	dimStyle    = lipgloss.NewStyle().Foreground(dim)
	accentStyle = lipgloss.NewStyle().Foreground(accent)
	warnStyle   = lipgloss.NewStyle().Foreground(warn)
	goodStyle   = lipgloss.NewStyle().Foreground(good)

	selectedRowStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)

	helpKeyStyle  = lipgloss.NewStyle().Foreground(dim).Bold(true)
	helpDescStyle = lipgloss.NewStyle().Foreground(subtle)
)

// ageRamp fades from green (fresh) toward grey; entry i covers ageSteps[i].
var ageRamp = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#1E824C", Dark: "#98C379"}), // today
	lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#4F8A58", Dark: "#8CAF78"}), // < 3 days
	lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#6F8C6F", Dark: "#7E9A80"}), // < 1 week
	lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#8A918A", Dark: "#6E8077"}), // < 2 weeks
}

// namePalette holds distinct hues that stay readable on light and dark
// backgrounds; each project name hashes to one of them.
var namePalette = []lipgloss.AdaptiveColor{
	{Light: "#C0392B", Dark: "#E06C75"}, // red
	{Light: "#B45309", Dark: "#D19A66"}, // orange
	{Light: "#8A6D00", Dark: "#E5C07B"}, // gold
	{Light: "#1E824C", Dark: "#98C379"}, // green
	{Light: "#0E7C7B", Dark: "#56B6C2"}, // teal
	{Light: "#0B6E99", Dark: "#61AFEF"}, // blue
	{Light: "#5A56E0", Dark: "#9E8CFC"}, // violet
	{Light: "#8E44AD", Dark: "#C678DD"}, // purple
	{Light: "#C2185B", Dark: "#E06C9F"}, // pink
	{Light: "#566573", Dark: "#ABB2BF"}, // slate
}

// nameColor deterministically maps a project name to a palette colour.
func nameColor(name string) lipgloss.AdaptiveColor {
	h := fnv.New32a()
	h.Write([]byte(name))
	return namePalette[h.Sum32()%uint32(len(namePalette))]
}

func nameStyle(name string) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(nameColor(name))
}

func helpEntry(key, desc string) string {
	return helpKeyStyle.Render(key) + " " + helpDescStyle.Render(desc)
}

func truncate(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}
