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
	colorClaude       = lipgloss.Color("#F9845C") // Claude orange
	colorPeach        = lipgloss.Color("#FAB387") // Peach
	colorTeal         = lipgloss.Color("#94E2D5") // Teal
	colorLavender     = lipgloss.Color("#B4BEFE") // Lavender
	colorFlamingo     = lipgloss.Color("#F2CDCD") // Flamingo
	colorSky          = lipgloss.Color("#89DCEB") // Sky
	colorSurface0     = lipgloss.Color("#313244") // Surface0
	colorSurface1     = lipgloss.Color("#45475A") // Surface1
	colorText         = lipgloss.Color("#CDD6F4") // Text
	colorSubtext0     = lipgloss.Color("#A6ADC8") // Subtext0
	colorSubtext1     = lipgloss.Color("#BAC2DE") // Subtext1

	// Header
	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	styleHeaderLine = lipgloss.NewStyle().
			Foreground(colorSubtext1)

	styleJudgeOn = lipgloss.NewStyle().
			Foreground(colorSuccess).
			Bold(true)

	styleJudgeOff = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleCost = lipgloss.NewStyle().
			Foreground(colorPrimary) // dim yellow

	// Panels — standard rounded border
	stylePanelBorder = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorBorder).
				Padding(0, 1)

	stylePanelBorderActive = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorActiveBorder).
				Padding(0, 1)

	// Soft border — lighter dashed verticals for top panels
	softBorderDef = lipgloss.Border{
		Top:         "─",
		Bottom:      "─",
		Left:        "┊",
		Right:       "┊",
		TopLeft:     "╭",
		TopRight:    "╮",
		BottomLeft:  "╰",
		BottomRight: "╯",
	}

	styleSoftBorder = lipgloss.NewStyle().
			Border(softBorderDef).
			BorderForeground(colorBorder).
			Padding(0, 1)

	styleSoftBorderActive = lipgloss.NewStyle().
				Border(softBorderDef).
				BorderForeground(colorActiveBorder).
				Padding(0, 1)

	stylePanelTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSecondary)

	// Claude sparkle
	styleClaudeSparkle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorClaude)

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

	styleDanger = lipgloss.NewStyle().
			Foreground(colorDanger).
			Bold(true)

	// Story status styles
	styleStoryPassed = lipgloss.NewStyle().
				Foreground(colorSuccess)

	styleStoryRunning = lipgloss.NewStyle().
				Foreground(colorClaude).
				Bold(true)

	styleStoryPending = lipgloss.NewStyle().
				Foreground(colorMuted)

	styleStoryFailed = lipgloss.NewStyle().
				Foreground(colorDanger)

	styleStoryID = lipgloss.NewStyle().
			Foreground(colorLavender).
			Bold(true)

	styleStoryTitle = lipgloss.NewStyle().
				Foreground(colorSubtext1)

	styleWorkerBadge = lipgloss.NewStyle().
				Foreground(colorSky).
				Bold(true)

	// Context panel section headers
	styleSectionHeader = lipgloss.NewStyle().
				Foreground(colorPeach).
				Bold(true)

	// Tag badges
	styleTagActive = lipgloss.NewStyle().
			Foreground(colorSurface0).
			Background(colorClaude).
			Bold(true).
			Padding(0, 1)

	styleTagInactive = lipgloss.NewStyle().
				Foreground(colorMuted).
				Padding(0, 1)

	// Status bar (stuck alert)
	styleStuckBar = lipgloss.NewStyle().
			Foreground(colorSurface0).
			Background(colorDanger).
			Bold(true)

	styleStuckBarDetail = lipgloss.NewStyle().
				Foreground(colorSurface0).
				Background(colorDanger)

	// Status line (vim-like, bottom of screen)
	styleStatusInfo = lipgloss.NewStyle().
			Foreground(colorText).
			Background(colorSurface1)

	styleStatusWarn = lipgloss.NewStyle().
			Foreground(colorSurface0).
			Background(colorPrimary).
			Bold(true)

	styleStatusError = lipgloss.NewStyle().
				Foreground(colorSurface0).
				Background(colorDanger).
				Bold(true)
)
