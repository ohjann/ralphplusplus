package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"github.com/eoghanhynes/ralph/internal/debuglog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/eoghanhynes/ralph/internal/costs"
	"github.com/eoghanhynes/ralph/internal/events"
	"github.com/eoghanhynes/ralph/internal/memory"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/roles"
	"github.com/eoghanhynes/ralph/internal/storystate"
)

// BuildPromptOpts holds optional parameters for BuildPrompt.
type BuildPromptOpts struct {
	Role           roles.Role
	AntiPatterns   []memory.AntiPattern
	MemoryDisabled bool // skip memory injection when config.Memory.Disabled is true
}

// BuildPrompt reads ralph-prompt.md, appends PRD context, story state, iteration constraint,
// judge feedback, and event context into the prompt.
func BuildPrompt(ralphHome, projectDir, storyID string, p *prd.PRD, opts ...BuildPromptOpts) (string, error) {
	// Determine which prompt template to load based on role
	var role roles.Role
	if len(opts) > 0 {
		role = opts[0].Role
	}

	promptFile := "ralph-prompt.md"
	if role != "" {
		promptFile = roles.DefaultConfig(role).PromptFile
	}

	base, err := os.ReadFile(filepath.Join(ralphHome, promptFile))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", promptFile, err)
	}

	prompt := string(base)

	// Inject PRD context if provided
	var story *prd.UserStory
	if p != nil {
		story = p.FindStory(storyID)
		prompt += buildPRDContext(p, storyID, story)
		prompt += buildStoryStateContext(projectDir, storyID)
	}

	// Inject anti-pattern warnings if any match the current story's files
	if len(opts) > 0 && len(opts[0].AntiPatterns) > 0 {
		storyFiles := collectStoryFiles(projectDir, storyID, story)
		if warning := buildAntiPatternWarnings(opts[0].AntiPatterns, storyFiles); warning != "" {
			prompt += warning
		}
	}

	// Inject learned context from markdown memory files
	if len(opts) == 0 || !opts[0].MemoryDisabled {
		if section := buildLearnedContextSection(ralphHome); section != "" {
			prompt += section
		}
	}

	// Architect role plans freely — skip the iteration constraint
	if role != roles.RoleArchitect {
		prompt += fmt.Sprintf(`

---
## THIS ITERATION
You MUST only work on story **%s**. Do NOT implement any other story. After completing %s, stop immediately.
If progress.md contains a [CONTEXT EXHAUSTED] entry for %s, continue from where it left off.`, storyID, storyID, storyID)
	}

	// Debugger role: inject most recent stuck detection info
	if role == roles.RoleDebugger {
		prompt += buildDebuggerStuckContext(projectDir, storyID)
	}

	// Inject judge feedback if present
	feedbackPath := filepath.Join(projectDir, ".ralph", fmt.Sprintf("judge-feedback-%s.md", storyID))
	if feedback, err := os.ReadFile(feedbackPath); err == nil && len(feedback) > 0 {
		prompt += "\n\n---\n## JUDGE FEEDBACK (MUST ADDRESS)\n" + string(feedback)
	}

	// Inject context from event log
	if evts, err := events.Load(projectDir); err == nil && len(evts) > 0 {
		if section := events.FormatContextSection(evts, storyID); section != "" {
			prompt += "\n\n---\n" + section
		}
	}

	return prompt, nil
}

// buildPRDContext generates the YOUR STORY, PROJECT CONTEXT, and OTHER STORIES sections.
func buildPRDContext(p *prd.PRD, storyID string, story *prd.UserStory) string {
	var b strings.Builder

	// YOUR STORY section
	if story != nil {
		b.WriteString("\n\n---\n## YOUR STORY\n")
		b.WriteString(fmt.Sprintf("**%s: %s**\n\n", story.ID, story.Title))
		b.WriteString(story.Description + "\n\n")
		b.WriteString("### Acceptance Criteria\n")
		for _, ac := range story.AcceptanceCriteria {
			b.WriteString(fmt.Sprintf("- %s\n", ac))
		}
		if story.Approach != "" {
			b.WriteString(fmt.Sprintf("\n### Implementation Approach\n%s\n", story.Approach))
		}
		if story.Notes != "" {
			b.WriteString(fmt.Sprintf("\n**Notes:** %s\n", story.Notes))
		}
	}

	// PROJECT CONTEXT section
	b.WriteString("\n\n---\n## PROJECT CONTEXT\n")
	b.WriteString(fmt.Sprintf("- **Project:** %s\n", p.Project))
	b.WriteString(fmt.Sprintf("- **Branch:** %s\n", p.BranchName))
	b.WriteString(fmt.Sprintf("- **Progress:** %d/%d stories complete\n", p.CompletedCount(), p.TotalCount()))

	// Constraints section
	if len(p.Constraints) > 0 {
		b.WriteString("\n### Project Constraints\n")
		for _, c := range p.Constraints {
			b.WriteString(fmt.Sprintf("- %s\n", c))
		}
	}

	// OTHER STORIES section
	b.WriteString("\n\n---\n## OTHER STORIES\n")
	for _, s := range p.UserStories {
		if s.ID == storyID {
			continue
		}
		status := "queued"
		if s.Passes {
			status = "✓"
		}
		b.WriteString(fmt.Sprintf("- %s: %s [%s]\n", s.ID, s.Title, status))
	}

	return b.String()
}

var validStoryID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func isValidStoryID(id string) bool {
	return validStoryID.MatchString(id)
}

// buildStoryStateContext loads story state, plan, and decisions and formats them.
func buildStoryStateContext(projectDir, storyID string) string {
	state, err := storystate.Load(projectDir, storyID)
	if err != nil || state.IterationCount == 0 {
		return ""
	}

	var b strings.Builder

	b.WriteString("\n\n---\n## Story State\n")
	b.WriteString(fmt.Sprintf("- **Status:** %s\n", state.Status))
	b.WriteString(fmt.Sprintf("- **Iteration:** %d\n", state.IterationCount))
	b.WriteString(fmt.Sprintf("- **Last Updated:** %s\n", state.LastUpdated.Format("2006-01-02 15:04:05")))

	if len(state.FilesTouched) > 0 {
		b.WriteString("- **Files Touched:** " + strings.Join(state.FilesTouched, ", ") + "\n")
	}
	if len(state.Subtasks) > 0 {
		b.WriteString("\n### Subtasks\n")
		for _, st := range state.Subtasks {
			check := "[ ]"
			if st.Done {
				check = "[x]"
			}
			b.WriteString(fmt.Sprintf("- %s %s\n", check, st.Description))
		}
	}
	if len(state.ErrorsEncountered) > 0 {
		b.WriteString("\n### Errors Encountered\n")
		for _, e := range state.ErrorsEncountered {
			b.WriteString(fmt.Sprintf("- **Error:** %s\n  **Resolution:** %s\n", e.Error, e.Resolution))
		}
	}
	if len(state.JudgeFeedback) > 0 {
		b.WriteString("\n### Judge Feedback\n")
		for _, jf := range state.JudgeFeedback {
			b.WriteString(fmt.Sprintf("- %s\n", jf))
		}
	}

	// Plan
	if plan, err := storystate.LoadPlan(projectDir, storyID); err == nil && plan != "" {
		b.WriteString("\n### Implementation Plan\n")
		b.WriteString(plan + "\n")
	}

	// Decisions
	if decisions, err := storystate.LoadDecisions(projectDir, storyID); err == nil && decisions != "" {
		b.WriteString("\n### Key Decisions\n")
		b.WriteString(decisions + "\n")
	}

	// User hint (injected from TUI when stuck)
	if hint, err := storystate.LoadHint(projectDir, storyID); err == nil && hint != "" {
		b.WriteString("\n### User Hint\n")
		b.WriteString("⚠ The user provided the following guidance after observing your previous attempt:\n")
		b.WriteString(hint + "\n")
		// Consume the hint so it's only used once
		storystate.ClearHint(projectDir, storyID)
	}

	return b.String()
}

// HasStuckInfo returns true if there are any stuck-*.json files for the given
// story in the project's .ralph/ directory. This is used to decide whether to
// dispatch the debugger role instead of the implementer on retry.
func HasStuckInfo(projectDir, storyID string) bool {
	ralphDir := filepath.Join(projectDir, ".ralph")
	entries, err := os.ReadDir(ralphDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "stuck-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ralphDir, e.Name()))
		if err != nil {
			continue
		}
		var info StuckInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}
		if info.StoryID == "" || info.StoryID == storyID {
			return true
		}
	}
	return false
}

// buildLearnedContextSection reads learnings.md and prd-learnings.md from
// {ralphHome}/memory/ and returns a formatted "Learned Context" section.
// Returns empty string if no memory files exist or all are empty.
func buildLearnedContextSection(ralphHome string) string {
	learnings, _ := memory.ReadLearnings(ralphHome)
	prdLearnings, _ := memory.ReadPRDLearnings(ralphHome)

	learnings = strings.TrimSpace(learnings)
	prdLearnings = strings.TrimSpace(prdLearnings)

	if learnings == "" && prdLearnings == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n---\n## Learned Context (from previous runs)\n")
	if learnings != "" {
		b.WriteString("\n### Cross-Story Learnings\n")
		b.WriteString(learnings + "\n")
	}
	if prdLearnings != "" {
		b.WriteString("\n### PRD-Specific Learnings\n")
		b.WriteString(prdLearnings + "\n")
	}
	return b.String()
}

// buildDebuggerStuckContext finds the most recent stuck-*.json file for the
// story and formats it as context for the debugger role. It also includes
// errors_encountered from the story's state.json for additional context.
func buildDebuggerStuckContext(projectDir, storyID string) string {
	ralphDir := filepath.Join(projectDir, ".ralph")
	entries, err := os.ReadDir(ralphDir)
	if err != nil {
		return ""
	}

	// Find the highest-numbered stuck-*.json file
	var latest *StuckInfo
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "stuck-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ralphDir, e.Name()))
		if err != nil {
			continue
		}
		var info StuckInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}
		// Filter by story ID if set, or take any
		if info.StoryID != "" && info.StoryID != storyID {
			continue
		}
		if latest == nil || info.Iteration > latest.Iteration {
			latest = &info
		}
	}

	if latest == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n---\n## STUCK DETECTION INFO\n")
	b.WriteString(fmt.Sprintf("- **Pattern:** %s\n", latest.Pattern))
	b.WriteString(fmt.Sprintf("- **Iteration:** %d\n", latest.Iteration))
	b.WriteString(fmt.Sprintf("- **Repeat count:** %d\n", latest.Count))
	if len(latest.Commands) > 0 {
		b.WriteString(fmt.Sprintf("- **Repeated commands:** %s\n", strings.Join(latest.Commands, ", ")))
	}

	// Include errors_encountered from state.json for additional debugging context
	ss, err := storystate.Load(projectDir, storyID)
	if err == nil && len(ss.ErrorsEncountered) > 0 {
		b.WriteString("\n## ERRORS ENCOUNTERED (from state.json)\n")
		for _, e := range ss.ErrorsEncountered {
			b.WriteString(fmt.Sprintf("- **Error:** %s\n", e.Error))
			if e.Resolution != "" {
				b.WriteString(fmt.Sprintf("  **Resolution:** %s\n", e.Resolution))
			}
		}
	}

	return b.String()
}

// RunClaudeOpts contains optional parameters for RunClaude.
type RunClaudeOpts struct {
	Iteration int
	StoryID   string
	Role      roles.Role
}

// RunClaudeResult holds the results from a RunClaude invocation.
type RunClaudeResult struct {
	TokenUsage    *costs.TokenUsage
	RateLimitInfo *costs.RateLimitInfo
}

// RunClaude executes claude with streaming JSON output, parsing events into
// a human-readable activity file for the TUI to display. Raw JSON is written
// to the log file for debugging. Returns accumulated token usage and rate limit
// info from the streaming response alongside any error.
func RunClaude(ctx context.Context, projectDir, prompt, logFilePath string, opts ...RunClaudeOpts) (*RunClaudeResult, error) {
	logFile, err := os.Create(logFilePath)
	if err != nil {
		return nil, fmt.Errorf("creating log file: %w", err)
	}
	defer logFile.Close()

	activityPath := activityPathFromLog(logFilePath)
	activityFile, err := os.Create(activityPath)
	if err != nil {
		return nil, fmt.Errorf("creating activity file: %w", err)
	}
	defer activityFile.Close()

	cmd := exec.CommandContext(ctx, "claude",
		"--dangerously-skip-permissions",
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
	)
	cmd.Dir = projectDir
	cmd.Stdin = strings.NewReader(prompt)
	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(logFile, &stderrBuf)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting claude: %w", err)
	}

	proc := &streamProcessor{activityFile: activityFile, projectDir: projectDir}
	if len(opts) > 0 {
		proc.iteration = opts[0].Iteration
		proc.storyID = opts[0].StoryID
		proc.isFixStory = strings.HasPrefix(opts[0].StoryID, "FIX-")
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // up to 10MB lines
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintln(logFile, line)
		proc.processLine(line)
	}

	usage := proc.tokenUsage()
	result := &RunClaudeResult{
		TokenUsage:    &usage,
		RateLimitInfo: proc.rateLimitInfo,
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if IsUsageLimitError(stderrBuf.String()) {
			return result, &UsageLimitError{Stderr: strings.TrimSpace(stderrBuf.String())}
		}
		return result, err
	}
	return result, nil
}

// collectStoryFiles gathers files associated with a story from state.json
// FilesTouched and the story description/acceptance criteria.
func collectStoryFiles(projectDir, storyID string, story *prd.UserStory) map[string]bool {
	files := make(map[string]bool)

	// From state.json FilesTouched
	if state, err := storystate.Load(projectDir, storyID); err == nil {
		for _, f := range state.FilesTouched {
			files[f] = true
		}
	}

	// From plan.md
	if plan, err := storystate.LoadPlan(projectDir, storyID); err == nil && plan != "" {
		for _, f := range extractFilePaths(plan) {
			files[f] = true
		}
	}

	// From story description and acceptance criteria
	if story != nil {
		for _, f := range extractFilePaths(story.Description) {
			files[f] = true
		}
		for _, ac := range story.AcceptanceCriteria {
			for _, f := range extractFilePaths(ac) {
				files[f] = true
			}
		}
	}

	return files
}

// filePathPattern matches file paths like internal/foo/bar.go or path/to/file.ts
var filePathPattern = regexp.MustCompile(`\b[\w./+-]+\.(?:go|ts|js|py|rs|java|yaml|yml|json|toml|md|sql|sh)\b`)

// extractFilePaths pulls file-like paths from text.
func extractFilePaths(text string) []string {
	return filePathPattern.FindAllString(text, -1)
}

// buildAntiPatternWarnings checks anti-patterns against story files and returns
// a formatted warning section. Returns empty string if no matches. Max 3 warnings.
func buildAntiPatternWarnings(patterns []memory.AntiPattern, storyFiles map[string]bool) string {
	if len(storyFiles) == 0 {
		return ""
	}

	type warning struct {
		file    string
		pattern memory.AntiPattern
	}

	var warnings []warning
	for _, ap := range patterns {
		for _, f := range ap.FilesAffected {
			if storyFiles[f] {
				warnings = append(warnings, warning{file: f, pattern: ap})
				break // one warning per anti-pattern
			}
		}
		if len(warnings) >= 3 {
			break
		}
	}

	if len(warnings) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n---\n## KNOWN ISSUES\n")
	for _, w := range warnings {
		b.WriteString(fmt.Sprintf("KNOWN ISSUE: %s has caused %s in %d stories. %s\n",
			w.file, w.pattern.Category, len(w.pattern.AffectedStories), w.pattern.Description))
	}

	return b.String()
}

// LogFilePath returns the log file path for a given iteration.
func LogFilePath(logDir string, iteration int) string {
	return filepath.Join(logDir, fmt.Sprintf("iteration-%d.log", iteration))
}

// ActivityFilePath returns the activity file path for a given iteration.
func ActivityFilePath(logDir string, iteration int) string {
	return filepath.Join(logDir, fmt.Sprintf("iteration-%d-activity.log", iteration))
}

// ReadLogTail reads the last n lines of a file. Returns empty string if file doesn't exist.
func ReadLogTail(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ReadActivityContent reads the activity file, keeping the tail if it's too large.
func ReadActivityContent(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := string(data)
	const maxSize = 256 * 1024
	if len(content) > maxSize {
		content = content[len(content)-maxSize:]
		if idx := strings.Index(content, "\n"); idx >= 0 {
			content = content[idx+1:]
		}
	}
	return content
}

// LogContainsComplete checks if the output contains the completion signal.
func LogContainsComplete(logPath string) bool {
	// Check activity file first (continuous text, most reliable)
	activityPath := activityPathFromLog(logPath)
	if data, err := os.ReadFile(activityPath); err == nil {
		if strings.Contains(string(data), "<promise>COMPLETE</promise>") {
			return true
		}
	}
	// Fallback: check raw log
	if data, err := os.ReadFile(logPath); err == nil {
		if strings.Contains(string(data), "<promise>COMPLETE</promise>") {
			return true
		}
	}
	return false
}

func activityPathFromLog(logPath string) string {
	return strings.TrimSuffix(logPath, ".log") + "-activity.log"
}

// ToolRecord tracks a tool invocation for stuck detection.
type ToolRecord struct {
	Tool     string
	Command  string
	FilePath string
}

// StuckInfo describes a detected stuck pattern.
type StuckInfo struct {
	Pattern   string   `json:"pattern"`
	Commands  []string `json:"repeated_commands"`
	Count     int      `json:"count"`
	Iteration int      `json:"iteration"`
	StoryID   string   `json:"story_id"`
}

// ReadStuckInfo reads a stuck info file for a given iteration.
func ReadStuckInfo(projectDir string, iteration int) *StuckInfo {
	path := filepath.Join(projectDir, ".ralph", fmt.Sprintf("stuck-%d.json", iteration))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var info StuckInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil
	}
	return &info
}

func writeStuckInfo(projectDir string, info StuckInfo) {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(projectDir, ".ralph", fmt.Sprintf("stuck-%d.json", info.Iteration))
	_ = os.WriteFile(path, data, 0o644)
}

// streamProcessor parses streaming JSON events from Claude and writes
// human-readable activity to a file.
type streamProcessor struct {
	activityFile *os.File
	currentBlock string // "thinking", "tool_use", "text"
	currentTool  string
	inputBuf     strings.Builder // accumulates tool input JSON
	syncCount    int             // counter for periodic syncing

	// Stuck detection
	recentTools []ToolRecord // ring buffer, cap 10
	projectDir  string
	iteration   int
	storyID     string
	isFixStory  bool

	// Token usage accumulation
	model        string
	inputTokens  int
	outputTokens int
	cacheRead    int
	cacheWrite   int
	numTurns     int
	durationMS   int

	// Rate limit info from rate_limit_event
	rateLimitInfo *costs.RateLimitInfo
}

// tokenUsage returns the accumulated token usage as a costs.TokenUsage.
func (sp *streamProcessor) tokenUsage() costs.TokenUsage {
	return costs.TokenUsage{
		InputTokens:  sp.inputTokens,
		OutputTokens: sp.outputTokens,
		CacheRead:    sp.cacheRead,
		CacheWrite:   sp.cacheWrite,
		Model:        sp.model,
		Provider:     "claude",
		NumTurns:     sp.numTurns,
		DurationMS:   sp.durationMS,
	}
}

// parseUsage extracts token counts from a usage object in streaming events.
func (sp *streamProcessor) parseUsage(usage map[string]interface{}) {
	if v, ok := usage["input_tokens"].(float64); ok {
		sp.inputTokens += int(v)
	}
	if v, ok := usage["output_tokens"].(float64); ok {
		sp.outputTokens += int(v)
	}
	if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
		sp.cacheWrite += int(v)
	}
	if v, ok := usage["cache_read_input_tokens"].(float64); ok {
		sp.cacheRead += int(v)
	}
}

// parseRateLimitInfo extracts rate limit data from a rate_limit_event.
func (sp *streamProcessor) parseRateLimitInfo(info map[string]interface{}) {
	rli := &costs.RateLimitInfo{}
	if status, ok := info["status"].(string); ok {
		rli.Status = status
	}
	if resetsAt, ok := info["resetsAt"].(float64); ok {
		rli.ResetsAt = time.Unix(int64(resetsAt), 0)
	}
	if rlType, ok := info["rateLimitType"].(string); ok {
		rli.RateLimitType = rlType
	}
	sp.rateLimitInfo = rli
}

func (sp *streamProcessor) processLine(line string) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return
	}

	// Claude Code CLI emits several top-level event types:
	//   "stream_event" — wraps raw API events (message_start, content_block_delta, etc.)
	//   "system" — init, hooks
	//   "assistant" — complete assistant message with usage
	//   "result" — final result with aggregated usage, num_turns, duration
	topType, _ := raw["type"].(string)

	// Handle top-level Claude Code CLI events that carry usage/metadata
	switch topType {
	case "result":
		if usage, ok := raw["usage"].(map[string]interface{}); ok {
			sp.parseUsage(usage)
		}
		if numTurns, ok := raw["num_turns"].(float64); ok {
			sp.numTurns = int(numTurns)
		}
		if durMS, ok := raw["duration_ms"].(float64); ok {
			sp.durationMS = int(durMS)
		}
		if model, ok := raw["model"].(string); ok && sp.model == "" {
			sp.model = model
		}
		return

	case "assistant":
		if msg, ok := raw["message"].(map[string]interface{}); ok {
			if model, ok := msg["model"].(string); ok && model != "<synthetic>" && sp.model == "" {
				sp.model = model
			}
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				sp.parseUsage(usage)
			}
		}
		return

	case "system":
		if model, ok := raw["model"].(string); ok && sp.model == "" {
			sp.model = model
		}
		return

	case "rate_limit_event":
		if info, ok := raw["rate_limit_info"].(map[string]interface{}); ok {
			sp.parseRateLimitInfo(info)
		}
		return
	}

	// Unwrap stream_event envelope for raw API events
	event := raw
	if topType == "stream_event" {
		inner, ok := raw["event"].(map[string]interface{})
		if !ok {
			return
		}
		event = inner
	}

	eventType, _ := event["type"].(string)

	switch eventType {
	case "message_start":
		// Extract model and initial usage from the message object
		if msg, ok := event["message"].(map[string]interface{}); ok {
			if model, ok := msg["model"].(string); ok {
				sp.model = model
			}
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				sp.parseUsage(usage)
			} else {
				debuglog.Log("warning: message_start event missing usage field")
			}
		}

	case "message_delta":
		// Extract incremental usage from delta
		if usage, ok := event["usage"].(map[string]interface{}); ok {
			sp.parseUsage(usage)
		} else {
			debuglog.Log("warning: message_delta event missing usage field")
		}

	case "content_block_start":
		block, ok := event["content_block"].(map[string]interface{})
		if !ok {
			return
		}
		blockType, _ := block["type"].(string)
		sp.currentBlock = blockType
		sp.inputBuf.Reset()

		switch blockType {
		case "thinking":
			sp.writeLine("\n── Thinking ─────────────────────")
		case "tool_use":
			name, _ := block["name"].(string)
			sp.currentTool = name
			sp.writeLine(fmt.Sprintf("\n── Tool: %s ─────────────────────", name))
		case "text":
			sp.writeLine("\n── Response ─────────────────────")
		}

	case "content_block_delta":
		delta, ok := event["delta"].(map[string]interface{})
		if !ok {
			return
		}
		deltaType, _ := delta["type"].(string)
		switch deltaType {
		case "thinking_delta":
			if text, ok := delta["thinking"].(string); ok && text != "" {
				sp.writeRaw(text)
			}
		case "text_delta":
			if text, ok := delta["text"].(string); ok && text != "" {
				sp.writeRaw(text)
			}
		case "input_json_delta":
			if partial, ok := delta["partial_json"].(string); ok {
				sp.inputBuf.WriteString(partial)
			}
		}

	case "content_block_stop":
		if sp.currentBlock == "tool_use" {
			inputJSON := sp.inputBuf.String()
			sp.writeToolSummary(inputJSON)
			sp.recordTool(inputJSON)
			sp.inputBuf.Reset()
		}
		sp.currentBlock = ""
	}
}

func (sp *streamProcessor) recordTool(inputJSON string) {
	if inputJSON == "" || sp.projectDir == "" {
		return
	}
	var input map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		return
	}

	rec := ToolRecord{Tool: sp.currentTool}
	if cmd, ok := input["command"].(string); ok {
		rec.Command = cmd
	}
	if fp, ok := input["file_path"].(string); ok {
		rec.FilePath = fp
	}

	// Ring buffer of 10
	if len(sp.recentTools) >= 10 {
		sp.recentTools = sp.recentTools[1:]
	}
	sp.recentTools = append(sp.recentTools, rec)

	sp.checkStuck()
}

func (sp *streamProcessor) checkStuck() {
	if len(sp.recentTools) < 3 {
		return
	}

	// Thresholds depend on story type
	bashThreshold := 3
	editThreshold := 4
	if sp.isFixStory {
		bashThreshold = 5
		editThreshold = 6
	}

	// Check for repeated bash commands
	cmdCounts := make(map[string]int)
	for _, r := range sp.recentTools {
		if r.Tool == "Bash" && r.Command != "" {
			// Normalize: trim to first 100 chars for comparison
			key := r.Command
			if len(key) > 100 {
				key = key[:100]
			}
			cmdCounts[key]++
		}
	}
	for cmd, count := range cmdCounts {
		if count >= bashThreshold {
			info := StuckInfo{
				Pattern:   "repeated_bash_command",
				Commands:  []string{cmd},
				Count:     count,
				Iteration: sp.iteration,
				StoryID:   sp.storyID,
			}
			writeStuckInfo(sp.projectDir, info)
			return
		}
	}

	// Check for repeated file edits
	fileCounts := make(map[string]int)
	for _, r := range sp.recentTools {
		if (r.Tool == "Edit" || r.Tool == "Write") && r.FilePath != "" {
			fileCounts[r.FilePath]++
		}
	}
	for fp, count := range fileCounts {
		if count >= editThreshold {
			info := StuckInfo{
				Pattern:   "repeated_file_edit",
				Commands:  []string{fp},
				Count:     count,
				Iteration: sp.iteration,
				StoryID:   sp.storyID,
			}
			writeStuckInfo(sp.projectDir, info)
			return
		}
	}
}

func (sp *streamProcessor) writeToolSummary(inputJSON string) {
	if inputJSON == "" {
		return
	}
	var input map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		return
	}
	if fp, ok := input["file_path"].(string); ok {
		sp.writeLine(fmt.Sprintf("  → %s", fp))
	}
	if cmd, ok := input["command"].(string); ok {
		if len(cmd) > 200 {
			cmd = cmd[:200] + "..."
		}
		sp.writeLine(fmt.Sprintf("  $ %s", cmd))
	}
	if pattern, ok := input["pattern"].(string); ok {
		sp.writeLine(fmt.Sprintf("  → pattern: %s", pattern))
	}
	if query, ok := input["query"].(string); ok {
		sp.writeLine(fmt.Sprintf("  → %s", query))
	}
	if oldStr, ok := input["old_string"].(string); ok {
		preview := oldStr
		if len(preview) > 80 {
			preview = preview[:80] + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", "\\n")
		sp.writeLine(fmt.Sprintf("  → replacing: %s", preview))
	}
}

func (sp *streamProcessor) writeLine(line string) {
	fmt.Fprintln(sp.activityFile, line)
	sp.activityFile.Sync()
}

func (sp *streamProcessor) writeRaw(text string) {
	fmt.Fprint(sp.activityFile, text)
	sp.syncCount++
	// Sync every 5 writes or on newlines for real-time display
	if sp.syncCount >= 5 || strings.Contains(text, "\n") {
		sp.activityFile.Sync()
		sp.syncCount = 0
	}
}
