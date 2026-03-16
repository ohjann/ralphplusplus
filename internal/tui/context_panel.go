package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/eoghanhynes/ralph/internal/costs"
)

// contextMode determines what the context panel shows.
type contextMode int

const (
	contextProgress contextMode = iota // default: progress.md
	contextWorktree                    // jj status
	contextJudge                       // judge results
	contextQuality                     // quality review assessment
	contextMemory                      // memory statistics and context
	contextCosts                       // cost tracking breakdown
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
	CostsContent    string
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
	case contextCosts:
		content = data.CostsContent
		if content == "" {
			content = styleMuted.Render("  No usage data yet")
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
		{contextCosts, "◎", "Usage", true},
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

// renderMemoryWithRetrieval combines memory stats content with retrieval results.
func renderMemoryWithRetrieval(statsContent string, retrieval *MemoryRetrievalMsg) string {
	if retrieval == nil || len(retrieval.DocRefs) == 0 {
		return statsContent
	}

	var sb strings.Builder
	sb.WriteString(statsContent)
	sb.WriteString("\n  Retrieved for " + retrieval.StoryID + "\n")
	sb.WriteString("  ─────────────────────\n")

	for _, ref := range retrieval.DocRefs {
		preview := ref.Content
		if len(preview) > 80 {
			preview = preview[:80] + "…"
		}
		// Replace newlines in preview for single-line display
		preview = strings.ReplaceAll(preview, "\n", " ")
		sb.WriteString(fmt.Sprintf("  %.2f  %-20s %s\n", ref.Score, ref.Collection, preview))
	}

	if retrieval.TotalFound > len(retrieval.DocRefs) {
		remaining := retrieval.TotalFound - len(retrieval.DocRefs)
		sb.WriteString(fmt.Sprintf("\n  … %d more (%s token budget)\n", remaining, formatTokens(retrieval.MaxTokens)))
	}

	return sb.String()
}

// formatTokens formats a token count with K/M suffixes.
func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// renderCostsContent builds the usage panel content from RunCosting and story display info.
// Works for both API-key users (shows tokens + costs) and Max subscribers (shows iterations + turns).
func renderCostsContent(rc *costs.RunCosting, stories []StoryDisplayInfo) string {
	if rc == nil {
		return ""
	}

	rc.Lock()
	defer rc.Unlock()

	if len(rc.Stories) == 0 {
		return ""
	}

	var sb strings.Builder

	// Determine if we have token data (API key) or just iteration data (Max subscription)
	hasTokenData := rc.TotalInputTokens > 0 || rc.TotalOutputTokens > 0

	// Compute aggregate metrics
	totalIters := 0
	totalTurns := 0
	totalDurationMS := 0
	for _, sc := range rc.Stories {
		totalIters += len(sc.Iterations)
		for _, ic := range sc.Iterations {
			totalTurns += ic.TokenUsage.NumTurns
			totalDurationMS += ic.TokenUsage.DurationMS
		}
	}

	// Summary header
	if hasTokenData {
		sb.WriteString(fmt.Sprintf("  Current Run: $%.2f\n", rc.TotalCost))
	}
	sb.WriteString(fmt.Sprintf("  Iterations: %d | Turns: %d", totalIters, totalTurns))
	if totalDurationMS > 0 {
		sb.WriteString(fmt.Sprintf(" | API time: %s", formatDurationMS(totalDurationMS)))
	}
	sb.WriteString("\n")
	sb.WriteString("  " + strings.Repeat("─", 40) + "\n")

	// Per-story breakdown
	for _, sdi := range stories {
		sc, ok := rc.Stories[sdi.ID]

		status := "queued"
		if sdi.Passed {
			status = "done"
		} else if sdi.Running {
			status = "running"
		} else if sdi.Failed {
			status = "failed"
		}

		if !ok {
			sb.WriteString(fmt.Sprintf("  %s (%s)%s—\n", sdi.ID, status, strings.Repeat(" ", max(1, 20-len(sdi.ID)-len(status)))))
			continue
		}

		iterCount := len(sc.Iterations)
		iterLabel := "iter"
		if iterCount != 1 {
			iterLabel = "iters"
		}
		storyTurns := 0
		for _, ic := range sc.Iterations {
			storyTurns += ic.TokenUsage.NumTurns
		}

		detail := fmt.Sprintf("%d %s, %d turns", iterCount, iterLabel, storyTurns)
		if hasTokenData {
			detail = fmt.Sprintf("$%.2f  %s", sc.TotalCost, detail)
		}

		sb.WriteString(fmt.Sprintf("  %s (%s)%s%s\n",
			sdi.ID, status,
			strings.Repeat(" ", max(1, 20-len(sdi.ID)-len(status))),
			detail))

		if len(sc.JudgeCosts) > 0 {
			sb.WriteString(fmt.Sprintf("    └─ Judge (%dx)\n", len(sc.JudgeCosts)))
		}
	}

	// Token counts (only shown for API key users)
	if hasTokenData {
		sb.WriteString("  " + strings.Repeat("─", 40) + "\n")
		sb.WriteString(fmt.Sprintf("  Tokens: %s in / %s out\n",
			formatTokens(rc.TotalInputTokens),
			formatTokens(rc.TotalOutputTokens)))

		cacheRate := rc.CacheHitRateUnlocked()
		sb.WriteString(fmt.Sprintf("  Cache hit rate: %.0f%%\n", cacheRate*100))
	}

	return sb.String()
}

// formatDurationMS formats milliseconds into a human-readable duration.
func formatDurationMS(ms int) string {
	switch {
	case ms >= 3_600_000:
		return fmt.Sprintf("%.1fh", float64(ms)/3_600_000)
	case ms >= 60_000:
		return fmt.Sprintf("%.1fm", float64(ms)/60_000)
	case ms >= 1_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1_000)
	default:
		return fmt.Sprintf("%dms", ms)
	}
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
