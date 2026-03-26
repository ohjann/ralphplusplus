package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/eoghanhynes/ralph/internal/costs"
	"github.com/eoghanhynes/ralph/internal/memory"
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
	contextSettings                    // settings editor
	contextModeCount                   // sentinel: total number of modes
)

func newContextViewport(width, height int) viewport.Model {
	vp := viewport.New(width, height)
	vp.SetContent("")
	return vp
}

type contextPanelData struct {
	Mode             contextMode
	ProgressContent  string
	ProgressChanged  bool
	WorktreeContent  string
	JudgeContent     string
	QualityContent   string
	MemoryContent    string
	CostsContent        string
	AntiPatternsContent string
	RateLimitContent    string
	Phase               phase
	Settings            *settingsState
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
			// Accept the cached render if it matches the current input OR if a
			// newer render is in-flight (dirtyInput set, debounce pending).
			if markdownCache.rendered != "" && (markdownCache.input == content || markdownCache.dirtyInput == content) {
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
		if data.RateLimitContent != "" {
			content = data.RateLimitContent + "\n" + content
		}
		if data.AntiPatternsContent != "" {
			content = content + "\n" + data.AntiPatternsContent
		}
	case contextSettings:
		if data.Settings != nil {
			content = renderSettingsPanel(*data.Settings, contentW)
		} else {
			content = styleMuted.Render("  Settings unavailable")
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
		{contextSettings, "⚙", "Settings", true},
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

// maxMarkdownLines is the maximum number of tail lines sent to glamour.
// Rendering only the tail keeps glamour fast as the progress file grows,
// since the viewport auto-scrolls to the bottom anyway.
const maxMarkdownLines = 150

// markdownCache caches the last rendered markdown.
// All fields are only accessed from the main Bubble Tea goroutine.
var markdownCache struct {
	// Cache key
	input string
	width int

	// Cache value
	rendered string

	// Sequence counter — incremented on every new render request.
	// Results from stale renders (seq < current) are discarded.
	seq uint64

	// Debounce: we note when content last changed and only dispatch
	// a render once it has settled for debounceInterval.
	lastChangeAt time.Time
	dirtyInput   string // content waiting to be rendered after debounce
	dirtyWidth   int
}

// markdownRenderedMsg is sent when async markdown rendering completes.
type markdownRenderedMsg struct {
	Input    string
	Width    int
	Rendered string
	Seq      uint64 // matches the seq at dispatch time; stale results are dropped
}

// markdownDebounceMsg fires after the debounce interval to check if it's
// time to actually kick off a render.
type markdownDebounceMsg struct {
	Seq uint64
}

const markdownDebounceInterval = 800 * time.Millisecond

// renderMarkdownAsync starts a background render and returns a Cmd.
// A fresh glamour.TermRenderer is created inside the goroutine so there
// is no shared mutable state — the previous design shared a cached renderer
// between concurrent Cmd goroutines which is not safe.
func renderMarkdownAsync(content string, width int, seq uint64) tea.Cmd {
	return safeCmd(func() tea.Msg {
		// Truncate to tail: only render the last maxMarkdownLines lines.
		// This keeps glamour fast as the progress file grows.
		truncated := truncateToTail(content, maxMarkdownLines)

		r, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return markdownRenderedMsg{Input: content, Width: width, Rendered: truncated, Seq: seq}
		}
		rendered, err := r.Render(truncated)
		if err != nil {
			return markdownRenderedMsg{Input: content, Width: width, Rendered: truncated, Seq: seq}
		}
		return markdownRenderedMsg{
			Input:    content,
			Width:    width,
			Rendered: strings.TrimRight(rendered, "\n"),
			Seq:      seq,
		}
	})
}

// maybeRenderMarkdown is called from Update() when progress content changes.
// It debounces: instead of rendering immediately, it records the change and
// schedules a debounce check. The actual render only fires once content has
// been stable for markdownDebounceInterval.
func maybeRenderMarkdown(content string, width int) tea.Cmd {
	// Exact cache hit — nothing to do.
	if content == markdownCache.input && width == markdownCache.width {
		return nil
	}

	now := time.Now()
	markdownCache.lastChangeAt = now
	markdownCache.dirtyInput = content
	markdownCache.dirtyWidth = width
	markdownCache.seq++
	seq := markdownCache.seq

	// Schedule a debounce check instead of rendering immediately.
	return tea.Tick(markdownDebounceInterval, func(time.Time) tea.Msg {
		return markdownDebounceMsg{Seq: seq}
	})
}

// handleMarkdownDebounce is called from Update() when a debounce timer fires.
// If no newer content arrived since this timer was scheduled, kick off the render.
func handleMarkdownDebounce(msg markdownDebounceMsg) tea.Cmd {
	// Stale debounce — a newer change superseded this one.
	if msg.Seq != markdownCache.seq {
		return nil
	}
	// Content settled — nothing new arrived during the debounce window.
	content := markdownCache.dirtyInput
	width := markdownCache.dirtyWidth
	if content == markdownCache.input && width == markdownCache.width {
		return nil // already rendered
	}
	return renderMarkdownAsync(content, width, markdownCache.seq)
}

// applyMarkdownRendered updates the cache when async rendering completes.
func applyMarkdownRendered(msg markdownRenderedMsg) {
	// Discard stale results — a newer render was requested.
	if msg.Seq < markdownCache.seq {
		return
	}
	markdownCache.input = msg.Input
	markdownCache.width = msg.Width
	markdownCache.rendered = msg.Rendered
}

// truncateToTail returns the last n lines of s. If s has fewer than n lines,
// it is returned unchanged. This avoids feeding glamour a huge document when
// only the tail is visible in the auto-scrolled viewport.
func truncateToTail(s string, n int) string {
	// Fast path: count newlines from the end.
	end := len(s)
	for count := 0; end > 0; {
		i := strings.LastIndexByte(s[:end], '\n')
		if i < 0 {
			break
		}
		count++
		if count >= n {
			return s[i+1:]
		}
		end = i
	}
	return s
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

// renderMemoryContent formats memory file stats for the memory tab.
func renderMemoryContent(stats []memory.MemoryFileInfo) string {
	if len(stats) == 0 {
		return styleMuted.Render("  No memory files")
	}

	var sb strings.Builder
	sb.WriteString("  Markdown Memory Files\n")
	sb.WriteString("  " + strings.Repeat("─", 40) + "\n")

	anyExists := false
	for _, s := range stats {
		if !s.Exists {
			sb.WriteString(fmt.Sprintf("  %s — not created yet\n", s.Name))
			continue
		}
		anyExists = true
		sizeStr := formatFileSize(s.SizeBytes)
		sb.WriteString(fmt.Sprintf("  %s — %s, %d entries\n", s.Name, sizeStr, s.EntryCount))
	}

	if !anyExists {
		sb.WriteString("\n")
		sb.WriteString(styleMuted.Render("  Memory files will be created after the first post-run synthesis."))
	}

	return sb.String()
}

// formatFileSize returns a human-friendly file size.
func formatFileSize(bytes int64) string {
	switch {
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
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

// renderRateLimitContent formats rate limit info for the usage panel.
func renderRateLimitContent(info *costs.RateLimitInfo) string {
	if info == nil || info.ResetsAt.IsZero() {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("  Plan Usage\n")
	sb.WriteString("  " + strings.Repeat("─", 40) + "\n")

	remaining := time.Until(info.ResetsAt)
	if remaining < 0 {
		remaining = 0
	}

	windowLabel := info.RateLimitType
	switch windowLabel {
	case "five_hour":
		windowLabel = "5-hour window"
	case "daily":
		windowLabel = "daily window"
	}

	sb.WriteString(fmt.Sprintf("  Window: %s\n", windowLabel))
	sb.WriteString(fmt.Sprintf("  Status: %s\n", info.Status))
	sb.WriteString(fmt.Sprintf("  Resets in: %s\n", formatDuration(remaining.Truncate(time.Second))))
	sb.WriteString("  " + strings.Repeat("─", 40) + "\n")

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

// categoryIcon returns a compact icon for an anti-pattern category.
func categoryIcon(category string) string {
	switch category {
	case "fragile_area":
		return "🔥"
	case "flaky_test":
		return "🎲"
	case "high_friction":
		return "🧱"
	case "common_oversight":
		return "👁"
	default:
		return "⚠"
	}
}

// renderAntiPatternsContent builds the anti-patterns section for the Usage tab.
func renderAntiPatternsContent(patterns []memory.AntiPattern) string {
	if len(patterns) == 0 {
		return "  Detected Anti-Patterns (0)\n  " + strings.Repeat("─", 40) + "\n  No anti-patterns detected"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("  Detected Anti-Patterns (%d)\n", len(patterns)))
	sb.WriteString("  " + strings.Repeat("─", 40) + "\n")

	const maxFiles = 3
	for _, ap := range patterns {
		icon := categoryIcon(ap.Category)
		// One line: icon description (Nx)
		sb.WriteString(fmt.Sprintf("  %s %s (%dx)\n", icon, ap.Description, ap.OccurrenceCount))

		// Affected files on next line, truncated
		if len(ap.FilesAffected) > 0 {
			shown := ap.FilesAffected
			extra := 0
			if len(shown) > maxFiles {
				extra = len(shown) - maxFiles
				shown = shown[:maxFiles]
			}
			fileStr := strings.Join(shown, ", ")
			if extra > 0 {
				fileStr += fmt.Sprintf(" +%d more", extra)
			}
			sb.WriteString(fmt.Sprintf("    files: %s\n", fileStr))
		}
	}

	return sb.String()
}
