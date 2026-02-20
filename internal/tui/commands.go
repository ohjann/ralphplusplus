package tui

import (
	"context"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/eoghanhynes/ralph/internal/archive"
	"github.com/eoghanhynes/ralph/internal/config"
	rexec "github.com/eoghanhynes/ralph/internal/exec"
	"github.com/eoghanhynes/ralph/internal/judge"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/runner"
)

func fastTickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
		return fastTickMsg{}
	})
}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func pollProgressCmd(path string) tea.Cmd {
	return func() tea.Msg {
		data, err := os.ReadFile(path)
		if err != nil {
			return progressContentMsg{}
		}
		return progressContentMsg{Content: string(data)}
	}
}

func pollActivityCmd(activityPath string) tea.Cmd {
	return func() tea.Msg {
		content := runner.ReadActivityContent(activityPath)
		return claudeActivityMsg{Content: content}
	}
}

func pollWorktreeCmd(ctx context.Context, dir string) tea.Cmd {
	return func() tea.Msg {
		out, _ := rexec.JJStatus(ctx, dir)
		return worktreeMsg{Content: out}
	}
}

func reloadPRDCmd(path string) tea.Cmd {
	return func() tea.Msg {
		p, err := prd.Load(path)
		if err != nil {
			return prdReloadedMsg{}
		}
		next := p.NextIncompleteStory()
		storyID := ""
		if next != nil {
			storyID = next.ID
		}
		return prdReloadedMsg{
			CompletedCount: p.CompletedCount(),
			TotalCount:     p.TotalCount(),
			AllComplete:    p.AllComplete(),
			CurrentStoryID: storyID,
		}
	}
}

func archiveCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		archived, _ := archive.CheckAndArchive(
			cfg.ProjectDir, cfg.LastBranchFile, cfg.ArchiveDir, cfg.PRDFile, cfg.ProgressFile,
		)
		_ = archive.TrackBranch(cfg.PRDFile, cfg.LastBranchFile)
		_ = archive.EnsureProgressFile(cfg.ProgressFile)
		return archiveDoneMsg{Archived: archived}
	}
}

func findNextStoryCmd(prdPath string) tea.Cmd {
	return func() tea.Msg {
		p, err := prd.Load(prdPath)
		if err != nil {
			return nextStoryMsg{AllDone: true}
		}
		if p.AllComplete() {
			return nextStoryMsg{AllDone: true}
		}
		next := p.NextIncompleteStory()
		if next == nil {
			return nextStoryMsg{AllDone: true}
		}
		return nextStoryMsg{StoryID: next.ID, StoryTitle: next.Title}
	}
}

func runClaudeCmd(ctx context.Context, cfg *config.Config, storyID string, iteration int) tea.Cmd {
	return func() tea.Msg {
		prompt, err := runner.BuildPrompt(cfg.RalphHome, storyID)
		if err != nil {
			return claudeDoneMsg{Err: err}
		}

		logPath := runner.LogFilePath(cfg.LogDir, iteration)
		err = runner.RunClaude(ctx, cfg.ProjectDir, prompt, logPath)

		completeSignal := runner.LogContainsComplete(logPath)

		return claudeDoneMsg{Err: err, CompleteSignal: completeSignal}
	}
}

func runJudgeCmd(ctx context.Context, cfg *config.Config, storyID, preRev string) tea.Cmd {
	return func() tea.Msg {
		result := judge.RunJudge(ctx, cfg.RalphHome, cfg.ProjectDir, cfg.PRDFile, storyID, preRev)
		return judgeDoneMsg{Result: result}
	}
}

func captureRevCmd(ctx context.Context, dir string) string {
	rev, _ := rexec.JJCurrentRev(ctx, dir)
	return rev
}
