package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	colorPrimary   = lipgloss.Color("#FF6F00") // Ralph orange
	colorSecondary = lipgloss.Color("#4FC3F7")
	colorMuted     = lipgloss.Color("#666666")
	colorSuccess   = lipgloss.Color("#66BB6A")
	colorDanger    = lipgloss.Color("#EF5350")
	colorBorder    = lipgloss.Color("#444444")
	colorActiveBorder = lipgloss.Color("#FF6F00")

	// Header
	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	styleHeaderLine = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#CCCCCC"))

	styleJudgeOn = lipgloss.NewStyle().
			Foreground(colorSuccess).
			Bold(true)

	styleJudgeOff = lipgloss.NewStyle().
			Foreground(colorMuted)

	// Panels
	stylePanelBorder = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorBorder).
				Padding(0, 1)

	stylePanelBorderActive = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorActiveBorder).
				Padding(0, 1)

	stylePanelTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSecondary)

	// Bottom panel
	styleLogPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	// Footer
	styleFooter = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleKey = lipgloss.NewStyle().
			Foreground(colorSecondary).
			Bold(true)

	// Progress bar
	styleProgressFilled = lipgloss.NewStyle().
				Foreground(colorSuccess)

	styleProgressEmpty = lipgloss.NewStyle().
				Foreground(colorMuted)

	// Phase indicators
	stylePhaseActive = lipgloss.NewStyle().
				Foreground(colorPrimary).
				Bold(true)

	stylePhaseDone = lipgloss.NewStyle().
			Foreground(colorSuccess)
)
