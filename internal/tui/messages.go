package tui

import (
	"github.com/eoghanhynes/ralph/internal/costs"
	"github.com/eoghanhynes/ralph/internal/judge"
	"github.com/eoghanhynes/ralph/internal/memory"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/quality"
	"github.com/eoghanhynes/ralph/internal/roles"
	"github.com/eoghanhynes/ralph/internal/runner"
)

// Phase transitions
type phase int

const (
	phaseInit phase = iota
	phaseIterating
	phaseClaudeRun
	phaseJudgeRun
	phasePlanning // Claude generating prd.json from plan file
	phaseReview   // User reviewing generated prd.json
	phaseDone
	phaseIdle
	phaseDagAnalysis    // Claude Code analyzing dependencies
	phaseParallel       // coordinator running workers
	phaseQualityReview  // running quality lens reviewers
	phaseQualityFix     // Claude fixing quality issues
	phaseQualityPrompt  // asking user whether to continue fixing
	phaseSummary        // generating final summary of all changes
	phaseResumePrompt   // asking user whether to resume from checkpoint
	phasePaused         // paused due to Claude usage limit — waiting for user
)

// Tick messages
type fastTickMsg struct{}   // 500ms — poll activity/progress
type tickMsg struct{}       // 2s — poll jj st, reload prd
type spriteTickMsg struct{} // 120ms — sprite animation

// Data refresh messages
type progressContentMsg struct{ Content string }
type worktreeMsg struct{ Content string }
type claudeActivityMsg struct{ Content string }
type prdReloadedMsg struct {
	CompletedCount int
	TotalCount     int
	AllComplete    bool
	CurrentStoryID string
	Stories        []prd.UserStory // cached for display info rebuilds without disk I/O
}

// Phase transition messages
type archiveDoneMsg struct{ Archived bool }
type nextStoryMsg struct {
	StoryID    string
	StoryTitle string
	AllDone    bool
}
type claudeDoneMsg struct {
	Err            error
	CompleteSignal bool                // <promise>COMPLETE</promise> found
	DocRefs        []memory.DocRef     // retrieved doc refs for confirmation tracking
	TokenUsage     *costs.TokenUsage   // accumulated token usage from streaming
	RateLimitInfo  *costs.RateLimitInfo // latest rate limit info from Claude CLI
	TotalFound     int                 // total retrieval results before token budget trim
	MaxTokens      int                 // token budget used for retrieval
	Role           roles.Role          // which role just completed (architect/implementer)
}
type judgeDoneMsg struct {
	Result judge.Result
}
type stuckDetectedMsg struct {
	Info runner.StuckInfo
}
type fixStoryGeneratedMsg struct {
	StoryID    string
	TokenUsage costs.TokenUsage
	Err        error
}
type planDoneMsg struct {
	Err error
}
type qualityReviewDoneMsg struct {
	Assessment quality.Assessment
	Err        error
}
type qualityFixDoneMsg struct {
	Err error
}
type summaryDoneMsg struct {
	Content string
	Err     error
}

// Memory / ChromaDB lifecycle messages
type chromaSetupDoneMsg struct {
	Err      error
	Sidecar  *memory.Sidecar
	Client   *memory.ChromaClient
}
type codebaseScanDoneMsg struct {
	Err error
}
type pipelineEmbedDoneMsg struct {
	StoryID string
	Err     error
}
type memoryStatsMsg struct {
	Content string
}

// MemoryRetrievalMsg carries retrieved memory DocRefs from BuildPrompt to the TUI.
type MemoryRetrievalMsg struct {
	StoryID    string
	DocRefs    []memory.DocRef
	TotalFound int // total results before token budget trim
	MaxTokens  int // token budget used
}

// Cost tracking
type costUpdateMsg struct {
	Usage   costs.TokenUsage
	StoryID string
}

// Rate limit info update
type rateLimitUpdateMsg struct {
	Info *costs.RateLimitInfo
}

// Status bar
type statusLevel int

const (
	statusInfo statusLevel = iota
	statusWarn
	statusError
)

type statusMsg struct {
	Text  string
	Level statusLevel
}

type statusClearMsg struct{}

// Terminal size
type windowSizeMsg struct {
	Width  int
	Height int
}
