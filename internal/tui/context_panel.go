package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// contextMode determines what the context panel shows.
type contextMode int

const (
	contextProgress contextMode = iota // default: progress.md
	contextWorktree                    // jj status
	contextJudge                       // judge results
	contextQuality                     // quality review assessment
	contextMemory                      // memory statistics and context
	contextModeCount                   // sentinel: total number of modes
)

func newContextViewport(width, height int) viewport.Model {
	vp := viewport.New(width, height)
	vp.SetContent("")
	return vp
}

type contextPanelData struct {
	Mode            contextMode
	ProgressContent string
	ProgressChanged bool
	WorktreeContent string
	JudgeContent    string
	QualityContent  string
	MemoryContent   string
	Phase           phase
}

func renderContextPanel(vp *viewport.Model, data contextPanelData, active bool, width, height int) string {
	style := styleSoftBorder
	if active {
		style = styleSoftBorderActive
	}

	contentW := max(width-4, 0)
	vpH := max(height-4, 0) // border(2) + title(1) + tabs(1)

	vp.Width = contentW
	vp.Height = vpH

	// Build tab bar
	tabs := renderContextTabs(data)

	// Build content based on mode
	var content string
	switch data.Mode {
	case contextProgress:
		content = data.ProgressContent
		if content == "" {
			content = styleMuted.Render("  Waiting for progress updates...")
		} else {
			content = renderMarkdown(content, contentW)
		}
	case contextJudge:
		content = data.JudgeContent
		if content == "" {
			content = styleMuted.Render("  No judge results yet")
		}
	case contextQuality:
		content = data.QualityContent
		if content == "" {
			content = styleMuted.Render("  No quality results yet")
		}
	case contextWorktree:
		if data.WorktreeContent != "" {
			content = renderWorktreeCompact(data.WorktreeContent)
		} else {
			content = styleMuted.Render("  No changes")
		}
	case contextMemory:
		content = data.MemoryContent
		if content == "" {
			content = styleMuted.Render("  Waiting for memory data...")
		}
	}

	vp.SetContent(content)
	if data.Mode == contextProgress && data.ProgressChanged {
		// Auto-scroll to bottom when new progress content arrives
		vp.GotoBottom()
	}

	body := tabs + "\n" + vp.View()
	body = clampLines(body, height-2)

	return style.MaxHeight(height).Render(body)
}

// renderContextTabs shows the tab bar with the active tab highlighted.
func renderContextTabs(data contextPanelData) string {
	type tab struct {
		mode  contextMode
		icon  string
		label string
		show  bool
	}

	tabs := []tab{
		{contextProgress, "◈", "Progress", true},
		{contextWorktree, "⌥", "Tree", true},
		{contextJudge, "⚖", "Judge", true},
		{contextQuality, "◇", "Quality", true},
		{contextMemory, "⧫", "Memory", true},
	}

	var parts []string
	for _, t := range tabs {
		if !t.show {
			continue
		}
		label := fmt.Sprintf(" %s %s ", t.icon, t.label)
		if t.mode == data.Mode {
			parts = append(parts, styleTagActive.Render(label))
		} else {
			parts = append(parts, styleTagInactive.Render(label))
		}
	}

	return strings.Join(parts, "")
}

func hasJudgeContent(data contextPanelData) bool {
	return data.JudgeContent != "" || data.Phase == phaseJudgeRun
}

func hasQualityContent(data contextPanelData) bool {
	return data.QualityContent != "" || data.Phase == phaseQualityReview || data.Phase == phaseQualityFix || data.Phase == phaseQualityPrompt
}

// markdownCache caches the last rendered markdown to avoid re-rendering
// on every View() cycle when the content hasn't changed.
var markdownCache struct {
	input    string
	width    int
	rendered string
}

// renderMarkdown renders markdown content for the TUI using glamour.
// Results are cached and only re-rendered when content or width changes.
func renderMarkdown(content string, width int) string {
	if content == markdownCache.input && width == markdownCache.width {
		return markdownCache.rendered
	}

	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return content
	}
	rendered, err := r.Render(content)
	if err != nil {
		return content
	}
	result := strings.TrimRight(rendered, "\n")

	markdownCache.input = content
	markdownCache.width = width
	markdownCache.rendered = result

	return result
}

// renderWorktreeCompact formats jj status output more compactly.
func renderWorktreeCompact(content string) string {
	lines := strings.Split(content, "\n")
	var sb strings.Builder

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Color-code based on status
		switch {
		case strings.HasPrefix(trimmed, "A ") || strings.HasPrefix(trimmed, "Added"):
			sb.WriteString(styleSuccess.Render("  + " + trimmed[2:]))
		case strings.HasPrefix(trimmed, "M ") || strings.HasPrefix(trimmed, "Modified"):
			sb.WriteString(lipgloss.NewStyle().Foreground(colorPeach).Render("  ~ " + trimmed[2:]))
		case strings.HasPrefix(trimmed, "D ") || strings.HasPrefix(trimmed, "Removed"):
			sb.WriteString(styleDanger.Render("  - " + trimmed[2:]))
		default:
			sb.WriteString("  " + trimmed)
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// autoSelectContextMode picks the best tab based on the current phase.
func autoSelectContextMode(p phase, judgeContent, qualityContent string) contextMode {
	switch p {
	case phaseJudgeRun:
		return contextJudge
	case phaseQualityReview, phaseQualityFix, phaseQualityPrompt:
		return contextQuality
	default:
		// If we have quality content and just finished, stay on quality
		if qualityContent != "" {
			return contextQuality
		}
		// If we have judge content, show it
		if judgeContent != "" {
			return contextJudge
		}
		return contextProgress
	}
}
