package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
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
			// Use pre-rendered markdown from cache (rendered async in Update).
			// Fall back to raw content if nothing rendered yet.
			if markdownCache.rendered != "" && (markdownCache.input == content || markdownCache.pending) {
				content = markdownCache.rendered
			}
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

// markdownCache caches the last rendered markdown and the renderer itself
// to avoid expensive re-creation on every content change.
// All fields are only accessed from the main Bubble Tea goroutine.
var markdownCache struct {
	input    string
	width    int
	rendered string

	// Cached renderer — only recreated when width changes.
	renderer      *glamour.TermRenderer
	rendererWidth int

	// Async rendering: when content changes, we render in the background
	// and return the stale cached result until the new one is ready.
	pending   bool
	pendingIn string
}

// markdownRenderedMsg is sent when async markdown rendering completes.
type markdownRenderedMsg struct {
	Input    string
	Width    int
	Rendered string
}

// renderMarkdownAsync starts a background render and returns a Cmd.
// The renderer is captured from the cache on the main goroutine and passed
// into the background closure so that no shared state is accessed concurrently.
func renderMarkdownAsync(content string, width int) tea.Cmd {
	// Prepare renderer on the main goroutine (cache access is safe here).
	renderer := markdownCache.renderer
	if renderer == nil || width != markdownCache.rendererWidth {
		r, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			// Can't create renderer — return raw content immediately.
			return func() tea.Msg {
				return markdownRenderedMsg{Input: content, Width: width, Rendered: content}
			}
		}
		renderer = r
		markdownCache.renderer = r
		markdownCache.rendererWidth = width
	}

	return func() tea.Msg {
		// Only uses the local `renderer` — no shared state access.
		rendered, err := renderer.Render(content)
		if err != nil {
			return markdownRenderedMsg{Input: content, Width: width, Rendered: content}
		}
		return markdownRenderedMsg{
			Input:    content,
			Width:    width,
			Rendered: strings.TrimRight(rendered, "\n"),
		}
	}
}

// maybeRenderMarkdown checks if markdown needs re-rendering and returns
// a Cmd to render async if so. Call from Update(), not View().
func maybeRenderMarkdown(content string, width int) tea.Cmd {
	// Exact cache hit — nothing to do.
	if content == markdownCache.input && width == markdownCache.width {
		return nil
	}

	// Already rendering this exact input — don't double-dispatch.
	if markdownCache.pending && content == markdownCache.pendingIn && width == markdownCache.rendererWidth {
		return nil
	}

	// New content — kick off async render.
	markdownCache.pending = true
	markdownCache.pendingIn = content
	return renderMarkdownAsync(content, width)
}

// applyMarkdownRendered updates the cache when async rendering completes.
func applyMarkdownRendered(msg markdownRenderedMsg) {
	markdownCache.input = msg.Input
	markdownCache.width = msg.Width
	markdownCache.rendered = msg.Rendered
	markdownCache.pending = false
	markdownCache.pendingIn = ""
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
