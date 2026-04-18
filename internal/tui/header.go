package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/ohjann/ralphplusplus/internal/costs"
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
	if m.phase == phaseParallel && (m.coord != nil || m.client != nil) {
		activeCount := m.daemonActiveCount()
		phaseStr = stylePhaseActive.Render(fmt.Sprintf("⚡ %d/%d workers", activeCount, m.cfg.Workers))
	}
	// Show connecting state when daemon client is present but not yet connected
	if m.client != nil && !m.daemonConnected {
		phaseStr = stylePhaseActive.Render("◌ Connecting to daemon...")
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
	elapsedBlock := fmt.Sprintf("⏱ %s", costs.FormatDuration(elapsed))

	// Show rate limit reset time if available, otherwise show cost or iteration count
	var costBlock string
	if m.rateLimitInfo != nil && !m.rateLimitInfo.ResetsAt.IsZero() {
		pct := rateLimitUsagePercent(m.rateLimitInfo)
		costBlock = styleCost.Render(fmt.Sprintf("Usage: %d%%", pct))
	} else if m.runCosting.TotalInputTokens > 0 || m.runCosting.TotalOutputTokens > 0 {
		costBlock = styleCost.Render(fmt.Sprintf("$%.2f", m.runCosting.TotalCost))
	} else {
		totalIters := len(m.runCosting.Stories)
		if totalIters > 0 {
			costBlock = styleCost.Render(fmt.Sprintf("%d stories tracked", totalIters))
		} else {
			costBlock = styleCost.Render("—")
		}
	}

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
	if m.cfg.NotifyTopic != "" && !m.notifier.IsDisabled() {
		badges = append(badges, lipgloss.NewStyle().Foreground(colorSuccess).Bold(true).Render("🔔 ntfy"))
	}
	if m.client != nil {
		if m.daemonConnected {
			uptime := ""
			if m.daemonState != nil && m.daemonState.Uptime != "" {
				uptime = " " + m.daemonState.Uptime
			}
			badges = append(badges, lipgloss.NewStyle().Foreground(colorSuccess).Render(
				fmt.Sprintf("⚙ daemon%s", uptime)))
		} else {
			badges = append(badges, styleDanger.Render("⚙ daemon ✗"))
		}
	}

	badgeStr := ""
	if len(badges) > 0 {
		badgeStr = "  │  " + strings.Join(badges, "  ")
	}

	line2 := fmt.Sprintf("%s  │  %s  │  %s%s", progressBlock, elapsedBlock, costBlock, badgeStr)

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
	case phasePaused:
		return styleDanger.Render("⏸ Usage limit — press Enter to resume")
	case phaseDone:
		doneStr := ""
		if m.allComplete {
			doneStr = styleSuccess.Render("✓ All stories complete!")
		} else if m.completionReason != "" {
			doneStr = styleDanger.Render("✗ " + m.completionReason)
		} else {
			doneStr = styleDanger.Render("✗ Some failed stories")
		}
		// Append plan quality score if available
		pq := m.daemonGetPlanQuality()
		if pq.TotalStories > 0 {
			score := pq.Score()
			scoreStyle := styleSuccess
			if score < 0.5 {
				scoreStyle = styleDanger
			} else if score < 0.8 {
				scoreStyle = lipgloss.NewStyle().Foreground(colorPeach)
			}
			doneStr += "  │  " + scoreStyle.Render(fmt.Sprintf("Plan: %.0f%%", score*100))
			doneStr += styleMuted.Render(fmt.Sprintf(" (%d first-pass, %d retried, %d failed)",
				pq.FirstPassCount, pq.RetryCount, pq.FailedCount))
		}
		return doneStr
	case phaseParallel:
		active := m.daemonActiveStoryIDs()
		if len(active) > 0 {
			return strings.Join(active, ", ")
		}
		if m.coord != nil || m.client != nil {
			return styleMuted.Render("Scheduling...")
		}
		return styleMuted.Render("Starting workers...")
	case phaseInit:
		return styleMuted.Render("Initializing...")
	case phaseSummary:
		return lipgloss.NewStyle().Foreground(colorTeal).Render("✎ Generating summary...")
	case phaseRetrospective:
		return lipgloss.NewStyle().Foreground(colorTeal).Render("⚡ Design retrospective...")
	case phaseResumePrompt:
		return lipgloss.NewStyle().Foreground(colorPrimary).Bold(true).Render("◇ Unfinished run — y resume, n retry, q quit")
	case phaseInteractive:
		return lipgloss.NewStyle().Foreground(colorPrimary).Render("⌨ Interactive — press t to add a task")
	default:
		if m.currentStoryID != "" {
			s := styleStoryID.Render(m.currentStoryID)
			if m.currentRole != "" {
				s += " " + styleMuted.Render("·") + " " + lipgloss.NewStyle().Foreground(colorTeal).Render(string(m.currentRole))
			}
			if m.currentStoryTitle != "" {
				s += " " + styleStoryTitle.Render(m.currentStoryTitle)
			}
			if strings.HasPrefix(m.currentStoryID, "FIX-") {
				s += " " + styleDanger.Render("[AUTO-FIX]")
			}
			return s
		}
		return styleMuted.Render("Preparing next story...")
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
	filledStr := strings.Repeat("█", filled)
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
	case phasePaused:
		return styleDanger.Render("⏸ Paused")
	case phaseRetrospective:
		return stylePhaseActive.Render("⚡ Retrospective")
	default:
		return ""
	}
}

// isLoopActive returns true when the ralph loop is actively working.
func isLoopActive(p phase) bool {
	switch p {
	case phaseIdle, phaseDone, phaseReview, phaseResumePrompt, phaseQualityPrompt, phasePaused:
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


// rateLimitUsagePercent computes what percentage of the rate limit window has elapsed.
func rateLimitUsagePercent(info *costs.RateLimitInfo) int {
	if info == nil || info.ResetsAt.IsZero() {
		return 0
	}
	var windowDuration time.Duration
	switch info.RateLimitType {
	case "five_hour":
		windowDuration = 5 * time.Hour
	default:
		windowDuration = 5 * time.Hour // sensible default
	}
	remaining := time.Until(info.ResetsAt)
	if remaining < 0 {
		remaining = 0
	}
	elapsed := windowDuration - remaining
	if elapsed < 0 {
		elapsed = 0
	}
	pct := int(float64(elapsed) / float64(windowDuration) * 100)
	if pct > 100 {
		pct = 100
	}
	return pct
}
