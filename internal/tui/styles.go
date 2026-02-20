package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors — Catppuccin Mocha palette
	colorPrimary      = lipgloss.Color("#F9E2AF") // Yellow
	colorSecondary    = lipgloss.Color("#89B4FA") // Blue
	colorMuted        = lipgloss.Color("#6C7086") // Overlay0
	colorSuccess      = lipgloss.Color("#A6E3A1") // Green
	colorDanger       = lipgloss.Color("#F38BA8") // Red
	colorBorder       = lipgloss.Color("#585B70") // Surface2
	colorActiveBorder = lipgloss.Color("#CBA6F7") // Mauve

	// Header
	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	styleHeaderLine = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#BAC2DE")) // Subtext1

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

	styleQuitConfirm = lipgloss.NewStyle().
				Foreground(colorDanger).
				Bold(true)

	styleSuccess = lipgloss.NewStyle().
			Foreground(colorSuccess).
			Bold(true)

	styleMuted = lipgloss.NewStyle().
			Foreground(colorMuted)
)
