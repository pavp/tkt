package tui

import "github.com/charmbracelet/lipgloss"

// Catppuccin Mocha, Herdr's default theme, so the board and Herdr's sidebar read as one tool.
const (
	ctpText     = lipgloss.Color("#cdd6f4")
	ctpSubtext  = lipgloss.Color("#a6adc8")
	ctpOverlay  = lipgloss.Color("#7f849c")
	ctpSurface2 = lipgloss.Color("#585b70")
	ctpSurface1 = lipgloss.Color("#45475a")
	ctpBase     = lipgloss.Color("#1e1e2e")
	ctpLavender = lipgloss.Color("#b4befe")
	ctpBlue     = lipgloss.Color("#89b4fa")
	ctpTeal     = lipgloss.Color("#94e2d5")
	ctpGreen    = lipgloss.Color("#a6e3a1")
	ctpYellow   = lipgloss.Color("#f9e2af")
	ctpPeach    = lipgloss.Color("#fab387")
	ctpRed      = lipgloss.Color("#f38ba8")
	ctpMauve    = lipgloss.Color("#cba6f7")
	ctpGray     = lipgloss.Color("#9399b2")
)

// What each color means on the board; styles use these, never a palette color directly.
const (
	colorText      = ctpText
	colorSubtle    = ctpSubtext
	colorMuted     = ctpOverlay
	colorBorder    = ctpSurface2
	colorCard      = ctpSurface1
	colorSelection = ctpSurface1
	colorAccent    = ctpBlue
	colorActive    = ctpLavender
	colorDone      = ctpGreen
	colorAttention = ctpPeach
	colorWarning   = ctpYellow
	colorError     = ctpRed
	colorReview    = ctpMauve
	colorSubtask   = ctpTeal
	colorOnBadge   = ctpBase
)

var (
	dim      = lipgloss.NewStyle().Foreground(colorMuted)
	warn     = lipgloss.NewStyle().Foreground(colorAttention)
	notice   = lipgloss.NewStyle().Foreground(colorWarning)
	failure  = lipgloss.NewStyle().Foreground(colorError)
	ok       = lipgloss.NewStyle().Foreground(colorDone)
	bold     = lipgloss.NewStyle().Bold(true)
	selected = lipgloss.NewStyle().Background(colorSelection).Foreground(colorText)
	box      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorBorder).Padding(0, 1)
	boxSel   = box.BorderForeground(colorAccent)
)
