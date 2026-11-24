package tui

import (
	"strconv"

	"github.com/charmbracelet/lipgloss"
)

// stackPlace puts a stacked pull request's step before its title and, when the one it sits on is elsewhere,
// its base branch after it.
func stackPlace(it item, title string, sel bool) string {
	if it.step > 0 {
		step := strconv.Itoa(it.step) + " "
		if !sel {
			step = dim.Render(step)
		}
		title = step + title
	}
	if it.on != "" {
		on := "  on " + it.on
		if !sel {
			on = dim.Render(on)
		}
		title += on
	}
	return title
}

// stackStatusStyle colors a stack's status as its rows' statuses read: red when it needs acting on, green when
// it can go in.
func stackStatusStyle(status string) lipgloss.Style {
	switch status {
	case "Ready to merge", "Approved":
		return lipgloss.NewStyle().Foreground(colorDone)
	case "CI failing", "Changes requested", "Conflicts":
		return lipgloss.NewStyle().Foreground(colorError)
	}
	return dim
}
