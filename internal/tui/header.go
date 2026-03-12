package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func renderHeader(m *Model, width int) string {
	running := isLoopActive(m.phase)
	pulse := pulsePhase()

	// Line 1: ❖ RALPH v0.1  ┃  ⚡ Claude running  ┃  AB-XXX: Title
	titleBlock := fmt.Sprintf("  %s %s %s",
		renderDiamond(running, pulse),
		styleTitle.Render("RALPH"),
		styleMuted.Render(m.version),
	)

	phaseStr := renderPhase(m.phase)
	if m.phase == phaseParallel && m.coord != nil {
		activeCount := m.coord.ActiveCount()
		phaseStr = stylePhaseActive.Render(fmt.Sprintf("⚡ %d/%d workers", activeCount, m.cfg.Workers))
	}

	storyStr := renderCurrentTask(m)

	line1 := fmt.Sprintf("%s  %s  %s  │  %s",
		titleBlock,
		styleMuted.Render("┃"),
		phaseStr,
		storyStr,
	)

	// Line 2: ██████░░░░ 3/5  │  ⏱ 2m 34s  │  Judge: ON  │  Workers: 3
	bar := renderProgressBar(m.animatedFill, 16)
	storiesLabel := fmt.Sprintf("%d/%d", m.completedStories, m.totalStories)
	progressBlock := fmt.Sprintf("  %s %s", bar, styleMuted.Render(storiesLabel))

	elapsed := time.Since(m.startTime).Truncate(time.Second)
	elapsedBlock := fmt.Sprintf("⏱ %s", formatDuration(elapsed))

	var badges []string
	if m.cfg.JudgeEnabled {
		badges = append(badges, styleJudgeOn.Render("⚖ Judge"))
	}
	if m.cfg.QualityReview {
		badges = append(badges, lipgloss.NewStyle().Foreground(colorTeal).Bold(true).Render("◇ Quality"))
	}
	if m.cfg.Workers > 1 {
		badges = append(badges, lipgloss.NewStyle().Foreground(colorSky).Bold(true).Render(
			fmt.Sprintf("⫘ %d Workers", m.cfg.Workers)))
	}

	badgeStr := ""
	if len(badges) > 0 {
		badgeStr = "  │  " + strings.Join(badges, "  ")
	}

	line2 := fmt.Sprintf("%s  │  %s%s", progressBlock, elapsedBlock, badgeStr)

	// Decorative separator
	sep := renderDecorativeSeparator(width, running, pulse)
	return lipgloss.JoinVertical(lipgloss.Left, line1, line2, sep)
}

func renderCurrentTask(m *Model) string {
	switch m.phase {
	case phaseIdle:
		return styleMuted.Render("Idle mode")
	case phasePlanning:
		return lipgloss.NewStyle().Foreground(colorPeach).Render("✦ Generating prd.json from plan...")
	case phaseReview:
		return lipgloss.NewStyle().Foreground(colorPrimary).Bold(true).Render("◇ Review prd.json — press Enter to execute")
	case phaseDagAnalysis:
		return lipgloss.NewStyle().Foreground(colorSky).Render("◌ Analyzing story dependencies...")
	case phaseQualityReview:
		return lipgloss.NewStyle().Foreground(colorTeal).Render(
			fmt.Sprintf("⚖ Quality review (round %d)...", m.qualityIteration))
	case phaseQualityFix:
		return lipgloss.NewStyle().Foreground(colorTeal).Render(
			fmt.Sprintf("⚡ Fixing quality issues (round %d)...", m.qualityIteration))
	case phaseQualityPrompt:
		return lipgloss.NewStyle().Foreground(colorPrimary).Bold(true).Render(
			"◇ Issues remain — Enter to continue, q to finish")
	case phaseDone:
		if m.allComplete {
			return styleSuccess.Render("✓ All stories complete!")
		}
		return styleDanger.Render("✗ Some failed stories")
	case phaseParallel:
		if m.coord != nil {
			active := m.coord.ActiveStoryIDs()
			if len(active) > 0 {
				return strings.Join(active, ", ")
			}
			return styleMuted.Render("Scheduling...")
		}
		return styleMuted.Render("Starting workers...")
	default:
		if m.currentStoryID != "" {
			s := styleStoryID.Render(m.currentStoryID)
			if m.currentStoryTitle != "" {
				s += " " + styleStoryTitle.Render(m.currentStoryTitle)
			}
			if strings.HasPrefix(m.currentStoryID, "FIX-") {
				s += " " + styleDanger.Render("[AUTO-FIX]")
			}
			return s
		}
		return styleMuted.Render("Waiting...")
	}
}

func renderProgressBar(fillRatio float64, barWidth int) string {
	filled := int(fillRatio * float64(barWidth))
	if filled < 0 {
		filled = 0
	}
	if filled > barWidth {
		filled = barWidth
	}
	empty := barWidth - filled

	// Use gradient blocks for a fancier look
	filledStr := ""
	for i := 0; i < filled; i++ {
		filledStr += "█"
	}
	emptyStr := strings.Repeat("░", empty)

	return styleProgressFilled.Render(filledStr) +
		styleProgressEmpty.Render(emptyStr)
}

func renderDecorativeSeparator(width int, running bool, pulse float64) string {
	sideWidth := (width - 3) / 2
	if sideWidth < 0 {
		sideWidth = 0
	}

	if !running {
		accent := styleClaudeSparkle.Render("✦")
		left := renderGradientLine(sideWidth, true)
		right := renderGradientLine(width-3-sideWidth, false)
		return left + " " + accent + " " + right
	}

	// Pulse the center accent in sync with the wave
	accentStyle := styleClaudeSparkle
	if pulse < 0.15 {
		accentStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
	} else if pulse < 0.35 {
		accentStyle = lipgloss.NewStyle().Bold(true).Foreground(colorPeach)
	}
	accent := accentStyle.Render("✦")

	left := renderPulseLine(sideWidth, true, pulse)
	right := renderPulseLine(width-3-sideWidth, false, pulse)
	return left + " " + accent + " " + right
}

// renderGradientLine creates a decorative line with varying dash styles.
func renderGradientLine(width int, fadeRight bool) string {
	if width <= 0 {
		return ""
	}

	heavy := lipgloss.NewStyle().Foreground(colorClaude)
	medium := lipgloss.NewStyle().Foreground(colorBorder)
	light := lipgloss.NewStyle().Foreground(colorSurface1)

	var sb strings.Builder
	for i := 0; i < width; i++ {
		var dist int
		if fadeRight {
			dist = width - 1 - i
		} else {
			dist = i
		}
		switch {
		case dist < 2:
			sb.WriteString(heavy.Render("━"))
		case dist < 5:
			sb.WriteString(medium.Render("─"))
		default:
			sb.WriteString(light.Render("┄"))
		}
	}
	return sb.String()
}

func renderPhase(p phase) string {
	switch p {
	case phaseInit:
		return stylePhaseActive.Render("◌ Initializing")
	case phaseIterating:
		return stylePhaseActive.Render("✦ Finding story")
	case phaseClaudeRun:
		return stylePhaseActive.Render("⚡ Claude running")
	case phaseJudgeRun:
		return stylePhaseActive.Render("⚖ Judge reviewing")
	case phasePlanning:
		return stylePhaseActive.Render("✦ Planning")
	case phaseReview:
		return stylePhaseActive.Render("◇ Review")
	case phaseDone:
		return stylePhaseDone.Render("✓ Complete")
	case phaseIdle:
		return stylePhaseDone.Render("◇ Idle")
	case phaseDagAnalysis:
		return stylePhaseActive.Render("◌ Analyzing DAG")
	case phaseParallel:
		return stylePhaseActive.Render("⚡ Parallel")
	case phaseQualityReview:
		return stylePhaseActive.Render("⚖ Quality Review")
	case phaseQualityFix:
		return stylePhaseActive.Render("⚡ Quality Fix")
	case phaseQualityPrompt:
		return stylePhaseActive.Render("◇ Review Prompt")
	default:
		return ""
	}
}

// isLoopActive returns true when the ralph loop is actively working.
func isLoopActive(p phase) bool {
	switch p {
	case phaseIdle, phaseDone, phaseReview, phaseResumePrompt, phaseQualityPrompt:
		return false
	}
	return true
}

// pulsePhase returns 0.0–1.0 position within a ~1.5s repeating cycle.
func pulsePhase() float64 {
	ms := time.Now().UnixMilli()
	const cycleMs = 1500
	return float64(ms%cycleMs) / float64(cycleMs)
}

// renderDiamond renders the ❖ with an electric flash when running.
func renderDiamond(running bool, p float64) string {
	if !running {
		return styleClaudeSparkle.Render("❖")
	}
	switch {
	case p < 0.15:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Render("❖")
	case p < 0.35:
		return lipgloss.NewStyle().Bold(true).Foreground(colorPeach).Render("❖")
	default:
		return styleClaudeSparkle.Render("❖")
	}
}

// renderPulseLine draws a separator segment with an electric arc radiating outward.
func renderPulseLine(width int, fadeRight bool, p float64) string {
	if width <= 0 {
		return ""
	}

	// Exponential burst: fast initial expansion then decelerating — like a voltage spike
	pulsePos := math.Pow(p, 0.35) * float64(width) * 1.3

	glow := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
	bright := lipgloss.NewStyle().Bold(true).Foreground(colorClaude)
	warm := lipgloss.NewStyle().Foreground(colorPeach)
	medium := lipgloss.NewStyle().Foreground(colorBorder)
	light := lipgloss.NewStyle().Foreground(colorSurface1)

	var sb strings.Builder
	for i := 0; i < width; i++ {
		// Distance from center (0 = near center ✦)
		var dist int
		if fadeRight {
			dist = width - 1 - i
		} else {
			dist = i
		}

		// Character based on position (matches original gradient)
		char := "┄"
		if dist < 2 {
			char = "━"
		} else if dist < 5 {
			char = "─"
		}

		// Distance from pulse front (negative = pulse already passed)
		delta := float64(dist) - pulsePos

		switch {
		case delta > -2 && delta < 2:
			// Pulse front — white hot
			sb.WriteString(glow.Render(char))
		case delta > -6 && delta < 0:
			// Just behind front — bright orange arc
			sb.WriteString(bright.Render(char))
		case delta > -10 && delta < 0:
			// Trailing warmth
			sb.WriteString(warm.Render(char))
		default:
			// Normal gradient
			switch {
			case dist < 2:
				sb.WriteString(bright.Render(char))
			case dist < 5:
				sb.WriteString(medium.Render(char))
			default:
				sb.WriteString(light.Render(char))
			}
		}
	}
	return sb.String()
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %02dm %02ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
