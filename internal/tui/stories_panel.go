package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/storystate"
	"github.com/eoghanhynes/ralph/internal/worker"
)

// StoryDisplayInfo holds everything needed to render a story row.
type StoryDisplayInfo struct {
	ID             string
	Title          string
	Passed         bool
	Running        bool
	Failed         bool
	WorkerID       worker.WorkerID
	StartTime      time.Time
	IterationCount int
	Role           string // current agent role (architect/implementer/debugger)
	Status         string // task status for interactive tasks (clarifying, queued, running, done, failed)
	IsInteractive  bool   // true for T-prefix interactive tasks
}

func newStoriesViewport(width, height int) viewport.Model {
	vp := viewport.New(width, height)
	vp.SetContent("")
	return vp
}

func renderStoriesPanel(vp *viewport.Model, stories []StoryDisplayInfo, active bool, width, height int, animFrame int, selectedIdx int, expandedID string, prdFile string) string {
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

	projectDir := filepath.Dir(prdFile)
	content := renderStoryList(stories, contentW, animFrame, active, selectedIdx, expandedID, projectDir)
	prevOffset := vp.YOffset
	vp.SetContent(content)
	vp.SetYOffset(prevOffset)

	body := title + "\n" + vp.View()
	body = clampLines(body, height-2)

	return style.MaxHeight(height).Render(body)
}

func renderStoryList(stories []StoryDisplayInfo, width int, animFrame int, panelActive bool, selectedIdx int, expandedID string, projectDir string) string {
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

	selectedStyle := lipgloss.NewStyle().Background(lipgloss.Color("#313244")) // Surface0

	for i, s := range stories {
		var statusIcon string
		var idStyle, titleStyle func(string) string

		if s.IsInteractive {
			// Interactive tasks use ⚡ marker and status-based rendering
			switch {
			case s.Passed || s.Status == "done":
				statusIcon = styleStoryPassed.Render("⚡")
				idStyle = func(s string) string { return styleStoryPassed.Render(s) }
				titleStyle = func(s string) string {
					return lipgloss.NewStyle().Foreground(colorMuted).Strikethrough(true).Render(s)
				}
			case s.Running || s.Status == "running":
				statusIcon = styleStoryRunning.Render("⚡")
				idStyle = func(s string) string { return styleStoryRunning.Render(s) }
				titleStyle = func(s string) string { return styleStoryTitle.Render(s) }
			case s.Failed || s.Status == "failed":
				statusIcon = styleStoryFailed.Render("⚡")
				idStyle = func(s string) string { return styleStoryFailed.Render(s) }
				titleStyle = func(s string) string { return styleStoryFailed.Render(s) }
			case s.Status == "clarifying":
				statusIcon = styleMuted.Render("⚡")
				idStyle = func(s string) string { return styleMuted.Render(s) }
				titleStyle = func(s string) string { return styleMuted.Render(s) }
			default: // queued or unknown
				statusIcon = styleStoryPending.Render("⚡")
				idStyle = func(s string) string { return styleStoryPending.Render(s) }
				titleStyle = func(s string) string { return styleStoryPending.Render(s) }
			}
		} else {
			// Standard PRD story markers
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
		}

		// Build the line: icon ID title [worker badge] [elapsed]
		line := fmt.Sprintf(" %s %s %s",
			statusIcon,
			idStyle(s.ID),
			titleStyle(truncate(s.Title, width-len(s.ID)-15)),
		)

		// Role and iteration count for running stories
		if s.Running && s.Role != "" {
			line += " " + styleMuted.Render(fmt.Sprintf("(%s)", s.Role))
		}
		if s.Running && s.IterationCount > 0 {
			line += " " + styleMuted.Render(fmt.Sprintf("(iter %d)", s.IterationCount))
		}

		// Status label for interactive tasks
		if s.IsInteractive && s.Status != "" && !s.Running {
			line += " " + styleMuted.Render(fmt.Sprintf("[%s]", s.Status))
		}

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

		// Highlight selected row when panel is active
		if panelActive && i == selectedIdx {
			// Cursor indicator
			line = "▸" + line[1:]
			line = selectedStyle.Width(width).Render(line)
		}

		sb.WriteString(line)
		sb.WriteString("\n")

		// Render expanded details if this story is expanded
		if s.ID == expandedID {
			sb.WriteString(renderStoryDetails(s.ID, projectDir, width))
			sb.WriteString("\n")
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

// renderStoryDetails renders inline details for an expanded story.
func renderStoryDetails(storyID, projectDir string, width int) string {
	state, err := storystate.Load(projectDir, storyID)
	if err != nil || state.StoryID == "" {
		return styleMuted.Render("    No state data available")
	}

	var sb strings.Builder
	indent := "    "

	// Iteration count
	sb.WriteString(indent)
	sb.WriteString(styleMuted.Render(fmt.Sprintf("Iterations: %d", state.IterationCount)))
	sb.WriteString("\n")

	// Status
	sb.WriteString(indent)
	sb.WriteString(styleMuted.Render(fmt.Sprintf("Status: %s", state.Status)))
	sb.WriteString("\n")

	// Subtask progress
	if len(state.Subtasks) > 0 {
		done := 0
		for _, st := range state.Subtasks {
			if st.Done {
				done++
			}
		}
		sb.WriteString(indent)
		sb.WriteString(styleMuted.Render(fmt.Sprintf("Subtasks: %d/%d done", done, len(state.Subtasks))))
		sb.WriteString("\n")
		for _, st := range state.Subtasks {
			check := "○"
			if st.Done {
				check = "✓"
			}
			desc := truncate(st.Description, width-8)
			sb.WriteString(indent + "  ")
			sb.WriteString(styleMuted.Render(fmt.Sprintf("%s %s", check, desc)))
			sb.WriteString("\n")
		}
	}

	// Files touched
	if len(state.FilesTouched) > 0 {
		sb.WriteString(indent)
		sb.WriteString(styleMuted.Render(fmt.Sprintf("Files: %d", len(state.FilesTouched))))
		sb.WriteString("\n")
		for _, f := range state.FilesTouched {
			sb.WriteString(indent + "  ")
			sb.WriteString(styleMuted.Render(truncate(f, width-8)))
			sb.WriteString("\n")
		}
	}

	// Judge feedback
	if len(state.JudgeFeedback) > 0 {
		sb.WriteString(indent)
		sb.WriteString(styleMuted.Render(fmt.Sprintf("Judge feedback: %d entries", len(state.JudgeFeedback))))
		sb.WriteString("\n")
		for _, fb := range state.JudgeFeedback {
			sb.WriteString(indent + "  ")
			sb.WriteString(styleMuted.Render(truncate(fb, width-8)))
			sb.WriteString("\n")
		}
	}

	return strings.TrimRight(sb.String(), "\n")
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
// sequentialIteration is the current iteration count for sequential (non-parallel) mode.
// currentRole is the agent role for the current story in serial mode.
func BuildStoryDisplayInfos(stories []prd.UserStory, currentStoryID string, coord interface {
	Workers() map[worker.WorkerID]*worker.Worker
}, phase phase, sequentialIteration int, currentRole string) []StoryDisplayInfo {
	// Build worker assignments map
	workerAssignments := make(map[string]worker.WorkerID)
	workerIterations := make(map[string]int)
	workerRoles := make(map[string]string)
	workerStartTimes := make(map[string]time.Time)
	if coord != nil {
		for wID, w := range coord.Workers() {
			if w.State == worker.WorkerRunning || w.State == worker.WorkerSetup || w.State == worker.WorkerJudging {
				workerAssignments[w.StoryID] = wID
				workerIterations[w.StoryID] = w.Iteration
				workerRoles[w.StoryID] = string(w.Role)
			}
		}
	}
	_ = workerStartTimes // future use

	// Separate PRD stories and interactive tasks for ordering
	var prdStories, interactiveStories []prd.UserStory
	for _, s := range stories {
		if strings.HasPrefix(s.ID, "T-") {
			interactiveStories = append(interactiveStories, s)
		} else {
			prdStories = append(prdStories, s)
		}
	}
	// PRD stories first, then interactive tasks
	ordered := append(prdStories, interactiveStories...)

	var infos []StoryDisplayInfo
	for _, s := range ordered {
		interactive := strings.HasPrefix(s.ID, "T-")
		info := StoryDisplayInfo{
			ID:            s.ID,
			Title:         s.Title,
			Passed:        s.Passes,
			IsInteractive: interactive,
		}

		// Set status for interactive tasks
		if interactive {
			switch {
			case s.Passes:
				info.Status = "done"
			default:
				info.Status = "queued"
			}
		}

		// Check if running
		if wID, ok := workerAssignments[s.ID]; ok {
			info.Running = true
			info.WorkerID = wID
			info.IterationCount = workerIterations[s.ID]
			info.Role = workerRoles[s.ID]
			if interactive {
				info.Status = "running"
			}
		} else if s.ID == currentStoryID && (phase == phaseClaudeRun || phase == phaseJudgeRun) {
			info.Running = true
			info.IterationCount = sequentialIteration
			info.Role = currentRole
			if interactive {
				info.Status = "running"
			}
		}

		infos = append(infos, info)
	}
	return infos
}
