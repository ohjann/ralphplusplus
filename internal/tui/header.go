package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func renderHeader(m *Model, width int) string {
	// Line 1: RALPH  Iter X/Y  |  US-XXX: Title
	iterStr := fmt.Sprintf("Iter %d/%d", m.iteration, m.cfg.MaxIterations)

	storyStr := "Waiting..."
	if m.currentStoryID != "" {
		storyStr = m.currentStoryID
		if m.currentStoryTitle != "" {
			storyStr += ": " + m.currentStoryTitle
		}
	}
	if m.phase == phaseIdle {
		storyStr = "Idle mode"
	} else if m.phase == phaseDone {
		if m.allComplete {
			storyStr = "All stories complete!"
		} else {
			storyStr = "Max iterations reached"
		}
	}

	line1 := fmt.Sprintf("  %s  %s  │  %s",
		styleTitle.Render("RALPH"),
		iterStr,
		storyStr,
	)

	// Line 2: Stories: ██░░ N/M  |  Elapsed: Xm Ys  |  Judge: ON/OFF
	bar := renderProgressBar(m.completedStories, m.totalStories, 10)
	storiesStr := fmt.Sprintf("Stories: %s %d/%d", bar, m.completedStories, m.totalStories)

	elapsed := time.Since(m.startTime).Truncate(time.Second)
	elapsedStr := fmt.Sprintf("Elapsed: %s", formatDuration(elapsed))

	judgeStr := styleJudgeOff.Render("Judge: OFF")
	if m.cfg.JudgeEnabled {
		judgeStr = styleJudgeOn.Render("Judge: ON")
	}

	phaseStr := renderPhase(m.phase)

	line2 := fmt.Sprintf("  %s  │  %s  │  %s  │  %s", storiesStr, elapsedStr, judgeStr, phaseStr)

	// Combine with separator
	sep := styleHeaderLine.Render(strings.Repeat("─", width))
	return lipgloss.JoinVertical(lipgloss.Left, line1, line2, sep)
}

func renderProgressBar(completed, total, barWidth int) string {
	if total == 0 {
		return styleProgressEmpty.Render(strings.Repeat("░", barWidth))
	}
	filled := barWidth * completed / total
	if filled > barWidth {
		filled = barWidth
	}
	empty := barWidth - filled
	return styleProgressFilled.Render(strings.Repeat("█", filled)) +
		styleProgressEmpty.Render(strings.Repeat("░", empty))
}

func renderPhase(p phase) string {
	switch p {
	case phaseInit:
		return stylePhaseActive.Render("Initializing")
	case phaseIterating:
		return stylePhaseActive.Render("Finding story")
	case phaseClaudeRun:
		return stylePhaseActive.Render("Claude running")
	case phaseJudgeRun:
		return stylePhaseActive.Render("Judge reviewing")
	case phaseDone:
		return stylePhaseDone.Render("Done")
	case phaseIdle:
		return stylePhaseDone.Render("Idle")
	default:
		return ""
	}
}

func formatDuration(d time.Duration) string {
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m > 0 {
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
