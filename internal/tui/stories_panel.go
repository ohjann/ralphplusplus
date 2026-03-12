package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/worker"
)

// StoryDisplayInfo holds everything needed to render a story row.
type StoryDisplayInfo struct {
	ID        string
	Title     string
	Passed    bool
	Running   bool
	Failed    bool
	WorkerID  worker.WorkerID
	StartTime time.Time
}

func newStoriesViewport(width, height int) viewport.Model {
	vp := viewport.New(width, height)
	vp.SetContent("")
	return vp
}

func renderStoriesPanel(vp *viewport.Model, stories []StoryDisplayInfo, active bool, width, height int, animFrame int) string {
	icon := styleClaudeSparkle.Render("◆")
	title := fmt.Sprintf("%s %s", icon, stylePanelTitle.Render("Stories"))

	style := styleSoftBorder
	if active {
		style = styleSoftBorderActive
	}

	contentW := max(width-4, 0)
	vpH := max(height-3, 0)

	vp.Width = contentW
	vp.Height = vpH

	content := renderStoryList(stories, contentW, animFrame)
	prevOffset := vp.YOffset
	vp.SetContent(content)
	vp.SetYOffset(prevOffset)

	body := title + "\n" + vp.View()
	body = clampLines(body, height-2)

	return style.MaxHeight(height).Render(body)
}

func renderStoryList(stories []StoryDisplayInfo, width int, animFrame int) string {
	if len(stories) == 0 {
		return styleMuted.Render("  No stories loaded")
	}

	var sb strings.Builder

	// Animated spinner frames for running stories
	spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinFrame := spinnerFrames[animFrame%len(spinnerFrames)]

	passedCount := 0
	totalCount := len(stories)
	for _, s := range stories {
		if s.Passed {
			passedCount++
		}
	}

	// Mini progress at top
	miniBar := renderMiniProgress(passedCount, totalCount, width-2)
	sb.WriteString(miniBar)
	sb.WriteString("\n")

	for i, s := range stories {
		var statusIcon string
		var idStyle, titleStyle func(string) string

		switch {
		case s.Passed:
			statusIcon = styleStoryPassed.Render("✓")
			idStyle = func(s string) string { return styleStoryPassed.Render(s) }
			titleStyle = func(s string) string {
				return lipgloss.NewStyle().Foreground(colorMuted).Strikethrough(true).Render(s)
			}
		case s.Running:
			statusIcon = styleStoryRunning.Render(spinFrame)
			idStyle = func(s string) string { return styleStoryRunning.Render(s) }
			titleStyle = func(s string) string { return styleStoryTitle.Render(s) }
		case s.Failed:
			statusIcon = styleStoryFailed.Render("✗")
			idStyle = func(s string) string { return styleStoryFailed.Render(s) }
			titleStyle = func(s string) string { return styleStoryFailed.Render(s) }
		default:
			statusIcon = styleStoryPending.Render("○")
			idStyle = func(s string) string { return styleStoryPending.Render(s) }
			titleStyle = func(s string) string { return styleStoryPending.Render(s) }
		}

		// Build the line: icon ID title [worker badge] [elapsed]
		line := fmt.Sprintf(" %s %s %s",
			statusIcon,
			idStyle(s.ID),
			titleStyle(truncate(s.Title, width-len(s.ID)-15)),
		)

		// Worker badge for parallel mode
		if s.Running && s.WorkerID > 0 {
			badge := styleWorkerBadge.Render(fmt.Sprintf("W%d", s.WorkerID))
			line += " " + badge
		}

		// Elapsed time for running stories
		if s.Running && !s.StartTime.IsZero() {
			elapsed := time.Since(s.StartTime).Truncate(time.Second)
			line += " " + styleMuted.Render(formatDuration(elapsed))
		}

		sb.WriteString(line)
		if i < len(stories)-1 {
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// renderMiniProgress renders a compact colored progress bar.
func renderMiniProgress(done, total, width int) string {
	if total == 0 {
		return ""
	}

	label := fmt.Sprintf(" %d/%d ", done, total)
	barWidth := width - lipgloss.Width(label) - 2
	if barWidth < 4 {
		barWidth = 4
	}

	filled := 0
	if total > 0 {
		filled = barWidth * done / total
	}
	if filled > barWidth {
		filled = barWidth
	}
	empty := barWidth - filled

	bar := styleProgressFilled.Render(strings.Repeat("━", filled)) +
		styleProgressEmpty.Render(strings.Repeat("─", empty))

	return " " + bar + styleMuted.Render(label)
}

func truncate(s string, maxLen int) string {
	if maxLen < 3 {
		maxLen = 3
	}
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

// BuildStoryDisplayInfos creates display info from the model state.
func BuildStoryDisplayInfos(stories []prd.UserStory, currentStoryID string, coord interface {
	Workers() map[worker.WorkerID]*worker.Worker
}, phase phase) []StoryDisplayInfo {
	// Build worker assignments map
	workerAssignments := make(map[string]worker.WorkerID)
	workerStartTimes := make(map[string]time.Time)
	if coord != nil {
		for wID, w := range coord.Workers() {
			if w.State == worker.WorkerRunning || w.State == worker.WorkerSetup || w.State == worker.WorkerJudging {
				workerAssignments[w.StoryID] = wID
			}
		}
	}
	_ = workerStartTimes // future use

	var infos []StoryDisplayInfo
	for _, s := range stories {
		info := StoryDisplayInfo{
			ID:     s.ID,
			Title:  s.Title,
			Passed: s.Passes,
		}

		// Check if running
		if wID, ok := workerAssignments[s.ID]; ok {
			info.Running = true
			info.WorkerID = wID
		} else if s.ID == currentStoryID && (phase == phaseClaudeRun || phase == phaseJudgeRun) {
			info.Running = true
		}

		infos = append(infos, info)
	}
	return infos
}
