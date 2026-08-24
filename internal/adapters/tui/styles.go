package tui

import "github.com/charmbracelet/lipgloss"

var paneStyle = lipgloss.NewStyle().Border(lipgloss.Border{
	Top:         "─",
	Bottom:      "─",
	Left:        "│",
	Right:       "│",
	TopLeft:     "┌",
	TopRight:    "┐",
	BottomLeft:  "└",
	BottomRight: "┘",
}).Padding(0, 1)
