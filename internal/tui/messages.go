package tui

import "github.com/eoghanhynes/ralph/internal/judge"

// Phase transitions
type phase int

const (
	phaseInit phase = iota
	phaseIterating
	phaseClaudeRun
	phaseJudgeRun
	phaseDone
)

// Tick messages
type fastTickMsg struct{} // 500ms — poll log/progress
type tickMsg struct{}     // 2s — poll jj st, reload prd

// Data refresh messages
type progressContentMsg struct{ Content string }
type worktreeMsg struct{ Content string }
type logContentMsg struct{ Content string }
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

// Terminal size
type windowSizeMsg struct {
	Width  int
	Height int
}
