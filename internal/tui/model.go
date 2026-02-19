package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/eoghanhynes/ralph/internal/config"
	"github.com/eoghanhynes/ralph/internal/judge"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/runner"
)

const (
	panelProgress = iota
	panelWorktree
	panelCount
)

const logTailLines = 5

type Model struct {
	cfg    *config.Config
	ctx    context.Context
	cancel context.CancelFunc

	// State
	phase            phase
	iteration        int
	currentStoryID   string
	currentStoryTitle string
	preRev           string
	completedStories int
	totalStories     int
	allComplete      bool
	exitCode         int
	startTime        time.Time

	// Panel content
	progressContent string
	worktreeContent string
	logContent      string

	// Active panel for scrolling
	activePanel int

	// Components
	progressVP  viewport.Model
	worktreeVP  viewport.Model
	spinner     spinner.Model

	// Terminal size
	width  int
	height int

	// Track if we should auto-scroll progress
	prevProgressLen int
}

func NewModel(cfg *config.Config) *Model {
	ctx, cancel := context.WithCancel(context.Background())
	return &Model{
		cfg:       cfg,
		ctx:       ctx,
		cancel:    cancel,
		phase:     phaseInit,
		startTime: time.Now(),
		spinner:   newSpinner(),
		progressVP: newProgressViewport(40, 10),
		worktreeVP: newWorktreeViewport(30, 10),
	}
}

func (m *Model) ExitCode() int {
	return m.exitCode
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		archiveCmd(m.cfg),
		m.spinner.Tick,
		fastTickCmd(),
		tickCmd(),
	)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch {
		case msg.String() == "q" || msg.String() == "ctrl+c":
			m.cancel()
			return m, tea.Quit
		case msg.String() == "tab":
			m.activePanel = (m.activePanel + 1) % panelCount
			return m, nil
		case msg.String() == "j" || msg.String() == "down":
			if m.activePanel == panelProgress {
				m.progressVP.LineDown(1)
			} else {
				m.worktreeVP.LineDown(1)
			}
			return m, nil
		case msg.String() == "k" || msg.String() == "up":
			if m.activePanel == panelProgress {
				m.progressVP.LineUp(1)
			} else {
				m.worktreeVP.LineUp(1)
			}
			return m, nil
		case msg.String() == "pgdown":
			if m.activePanel == panelProgress {
				m.progressVP.ViewDown()
			} else {
				m.worktreeVP.ViewDown()
			}
			return m, nil
		case msg.String() == "pgup":
			if m.activePanel == panelProgress {
				m.progressVP.ViewUp()
			} else {
				m.worktreeVP.ViewUp()
			}
			return m, nil
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	// --- Fast tick: poll log + progress ---
	case fastTickMsg:
		cmds = append(cmds, fastTickCmd())
		cmds = append(cmds, pollProgressCmd(m.cfg.ProgressFile))
		if m.phase == phaseClaudeRun || m.phase == phaseJudgeRun {
			logPath := runner.LogFilePath(m.cfg.LogDir, m.iteration)
			cmds = append(cmds, pollLogCmd(logPath, logTailLines))
		}

	// --- Slow tick: poll worktree + prd ---
	case tickMsg:
		cmds = append(cmds, tickCmd())
		cmds = append(cmds, pollWorktreeCmd(m.ctx, m.cfg.ProjectDir))
		cmds = append(cmds, reloadPRDCmd(m.cfg.PRDFile))

	// --- Data updates ---
	case progressContentMsg:
		m.progressContent = msg.Content
		newLen := len(msg.Content)
		m.progressVP.SetContent(msg.Content)
		// Auto-scroll if new content
		if newLen > m.prevProgressLen {
			m.progressVP.GotoBottom()
		}
		m.prevProgressLen = newLen

	case worktreeMsg:
		m.worktreeContent = msg.Content

	case logContentMsg:
		m.logContent = msg.Content

	case prdReloadedMsg:
		m.completedStories = msg.CompletedCount
		m.totalStories = msg.TotalCount

	// --- Phase transitions ---
	case archiveDoneMsg:
		m.phase = phaseIterating
		cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))

	case nextStoryMsg:
		if msg.AllDone {
			m.phase = phaseDone
			m.allComplete = true
			m.exitCode = 0
			return m, tea.Quit
		}
		m.iteration++
		if m.iteration > m.cfg.MaxIterations {
			m.phase = phaseDone
			m.allComplete = false
			m.exitCode = 1
			return m, tea.Quit
		}
		m.currentStoryID = msg.StoryID
		m.currentStoryTitle = msg.StoryTitle
		m.phase = phaseClaudeRun
		m.logContent = ""

		// Capture revision for judge diff baseline
		if m.cfg.JudgeEnabled {
			m.preRev = captureRevCmd(m.ctx, m.cfg.ProjectDir)
		}

		cmds = append(cmds, runClaudeCmd(m.ctx, m.cfg, msg.StoryID, m.iteration))

	case claudeDoneMsg:
		if msg.Err != nil {
			// Context cancelled = user quit
			if m.ctx.Err() != nil {
				return m, tea.Quit
			}
			// Claude error — continue to next iteration
		}

		if msg.CompleteSignal {
			m.phase = phaseDone
			m.allComplete = true
			m.exitCode = 0
			return m, tea.Quit
		}

		// Judge check
		if m.cfg.JudgeEnabled && m.currentStoryID != "" {
			// Check if story now passes
			cmds = append(cmds, m.handleJudgeCheck())
			if len(cmds) > 0 {
				return m, tea.Batch(cmds...)
			}
		}

		// No judge or story didn't pass yet — next iteration
		m.phase = phaseIterating
		cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))

	case judgeDoneMsg:
		if msg.Result.Passed {
			judge.ClearRejectionCount(m.cfg.ProjectDir, m.currentStoryID)
		} else {
			judge.IncrementRejectionCount(m.cfg.ProjectDir, m.currentStoryID)
		}
		// Either way, move to next iteration
		m.phase = phaseIterating
		cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) handleJudgeCheck() tea.Cmd {
	// Reload PRD to check if story passes
	p, err := prd.Load(m.cfg.PRDFile)
	if err != nil {
		return nil
	}
	story := p.FindStory(m.currentStoryID)
	if story == nil || !story.Passes {
		return nil
	}

	// Story claims to pass — run judge
	rejections := judge.GetRejectionCount(m.cfg.ProjectDir, m.currentStoryID)
	if rejections >= m.cfg.JudgeMaxRejections {
		// Auto-pass
		judge.AppendAutoPass(m.cfg.ProgressFile, m.currentStoryID, rejections)
		judge.ClearRejectionCount(m.cfg.ProjectDir, m.currentStoryID)
		return nil
	}

	m.phase = phaseJudgeRun
	return runJudgeCmd(m.ctx, m.cfg, m.currentStoryID, m.preRev)
}

func (m *Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	// Layout calculations
	headerHeight := 3
	footerHeight := 1
	logPanelHeight := 3
	middleHeight := m.height - headerHeight - footerHeight - logPanelHeight - 1

	if middleHeight < 3 {
		middleHeight = 3
	}

	progressWidth := m.width * 60 / 100
	worktreeWidth := m.width - progressWidth

	// Render sections
	header := renderHeader(m, m.width)

	progressPanel := renderProgressPanel(
		m.progressVP,
		m.activePanel == panelProgress,
		progressWidth,
		middleHeight,
	)

	worktreePanel := renderWorktreePanel(
		m.worktreeVP,
		m.worktreeContent,
		m.activePanel == panelWorktree,
		worktreeWidth,
		middleHeight,
	)

	middle := lipgloss.JoinHorizontal(lipgloss.Top, progressPanel, worktreePanel)

	claudeRunning := m.phase == phaseClaudeRun || m.phase == phaseJudgeRun
	logPanel := renderLogPanel(m.spinner, m.logContent, claudeRunning, m.width)

	footer := renderFooter(m.width)

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		middle,
		logPanel,
		footer,
	)
}

func renderFooter(width int) string {
	help := styleKey.Render("q") + styleFooter.Render(": quit  ") +
		styleKey.Render("tab") + styleFooter.Render(": switch panel  ") +
		styleKey.Render("j/k") + styleFooter.Render(": scroll")
	return "  " + help
}

