package tui

import (
	"github.com/eoghanhynes/ralph/internal/judge"
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
	phaseDagAnalysis // Claude Code analyzing dependencies
	phaseParallel    // coordinator running workers
)

// Tick messages
type fastTickMsg struct{} // 500ms — poll activity/progress
type tickMsg struct{}     // 2s — poll jj st, reload prd

// Data refresh messages
type progressContentMsg struct{ Content string }
type worktreeMsg struct{ Content string }
type claudeActivityMsg struct{ Content string }
type prdReloadedMsg struct {
	CompletedCount int
	TotalCount     int
	AllComplete    bool
	CurrentStoryID string
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
	CompleteSignal bool // <promise>COMPLETE</promise> found
}
type judgeDoneMsg struct {
	Result judge.Result
}
type stuckDetectedMsg struct {
	Info runner.StuckInfo
}
type fixStoryGeneratedMsg struct {
	StoryID string
	Err     error
}
type planDoneMsg struct {
	Err error
}

// Terminal size
type windowSizeMsg struct {
	Width  int
	Height int
}
