package tui

import (
	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/judge"
	"github.com/ohjann/ralphplusplus/internal/memory"
	"github.com/ohjann/ralphplusplus/internal/prd"
	"github.com/ohjann/ralphplusplus/internal/quality"
	"github.com/ohjann/ralphplusplus/internal/roles"
	"github.com/ohjann/ralphplusplus/internal/runner"
	"github.com/ohjann/ralphplusplus/internal/worker"
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
	phaseInteractive    // idle-but-accepting-input; coexists with running workers
)

// workerUpdateMsg wraps a worker update for the TUI message system.
type workerUpdateMsg struct {
	Update worker.WorkerUpdate
}

// Tick messages
type fastTickMsg struct{}          // 500ms — poll activity/progress
type tickMsg struct{}              // 2s — poll jj st, reload prd
type spriteTickMsg struct{}        // 120ms — sprite animation
type usageLimitResumeMsg struct{} // auto-resume after usage limit reset

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
	TokenUsage     *costs.TokenUsage   // accumulated token usage from streaming
	RateLimitInfo  *costs.RateLimitInfo // latest rate limit info from Claude CLI
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
type synthesisDoneMsg struct {
	Err error
}
type dreamDoneMsg struct {
	Err error
}

// Memory stats message
type memoryStatsMsg struct {
	Stats []memory.MemoryFileInfo
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

// scheduleReadyDoneMsg is sent after ScheduleReady runs asynchronously so the
// event loop isn't blocked by fusion complexity LLM calls.
type scheduleReadyDoneMsg struct {
	Launched int
}

// clarifyResultMsg carries the result of a lightweight Claude clarification call.
// If Ready is true, the task is clear and should proceed to story creation.
// Otherwise, Questions contains up to 3 clarifying questions to show the user.
type clarifyResultMsg struct {
	TaskText  string   // original task text
	Ready     bool     // true if Claude said READY
	Questions []string // clarifying questions (if not ready)
	Err       error    // non-nil if the call failed
}

// Terminal size
type windowSizeMsg struct {
	Width  int
	Height int
}
