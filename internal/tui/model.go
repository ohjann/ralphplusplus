package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"
	"github.com/eoghanhynes/ralph/internal/config"
	"github.com/eoghanhynes/ralph/internal/coordinator"
	"github.com/eoghanhynes/ralph/internal/dag"
	"github.com/eoghanhynes/ralph/internal/events"
	"github.com/eoghanhynes/ralph/internal/judge"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/quality"
	"github.com/eoghanhynes/ralph/internal/runner"
	"github.com/eoghanhynes/ralph/internal/worker"
)

const (
	panelProgress = iota
	panelWorktree
	panelJudge
	panelClaude
	panelCount
)

type Model struct {
	cfg     *config.Config
	version string
	ctx     context.Context
	cancel  context.CancelFunc

	// State
	phase            phase
	iteration        int
	currentStoryID   string
	currentStoryTitle string
	preRevs          []judge.DirRev
	completedStories int
	totalStories     int
	allComplete      bool
	exitCode         int
	startTime        time.Time
	confirmQuit      bool

	// Panel content
	progressContent string
	worktreeContent string
	claudeContent   string
	judgeContent    string

	// Active panel for scrolling
	activePanel int

	// Components
	progressVP  viewport.Model
	worktreeVP  viewport.Model
	judgeVP     viewport.Model
	claudeVP    viewport.Model
	spinner     spinner.Model

	// Terminal size
	width  int
	height int

	// Track if we should auto-scroll
	prevProgressLen int
	prevClaudeLen   int
	prevJudgeLen    int

	// Spring-animated progress bar
	progressSpring harmonica.Spring
	animatedFill   float64 // current animated fill ratio (0.0–1.0)
	fillVelocity   float64 // spring velocity

	// Parallel execution
	coord            *coordinator.Coordinator
	storyDAG         *dag.DAG
	activeWorkerView worker.WorkerID // which worker's output to show in Claude panel

	// Quality review
	qualityIteration  int
	lastAssessment    *quality.Assessment
}

func NewModel(cfg *config.Config, version string) *Model {
	ctx, cancel := context.WithCancel(context.Background())
	return &Model{
		cfg:            cfg,
		version:        version,
		ctx:            ctx,
		cancel:         cancel,
		phase:          phaseInit,
		startTime:      time.Now(),
		spinner:        newSpinner(),
		progressVP:     newProgressViewport(40, 10),
		worktreeVP:     newWorktreeViewport(30, 10),
		judgeVP:        newJudgeViewport(30, 10),
		claudeVP:       newClaudeViewport(80, 20),
		progressSpring: harmonica.NewSpring(harmonica.FPS(30), 6.0, 0.5),
	}
}

func (m *Model) ExitCode() int {
	return m.exitCode
}

func (m *Model) Init() tea.Cmd {
	if m.cfg.IdleMode {
		m.phase = phaseIdle
		return tea.Batch(
			m.spinner.Tick,
			fastTickCmd(),
			tickCmd(),
		)
	}
	if m.cfg.PlanFile != "" {
		m.phase = phasePlanning
		return tea.Batch(
			planCmd(m.ctx, m.cfg),
			m.spinner.Tick,
			fastTickCmd(),
			tickCmd(),
		)
	}
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

		// Recompute viewport dimensions so SetContent wraps at the correct width
		available := m.height - 4 // header(3) + footer(1)
		if available < 10 {
			available = 10
		}
		topHeight := available * 35 / 100
		if topHeight < 5 {
			topHeight = 5
		}
		claudeHeight := available - topHeight

		progressWidth := m.width * 40 / 100
		worktreeWidth := m.width * 30 / 100
		judgeWidth := m.width - progressWidth - worktreeWidth

		m.progressVP.Width = progressWidth - 4 // border(2) + padding(2)
		m.progressVP.Height = topHeight - 3    // border(2) + title(1)
		m.worktreeVP.Width = worktreeWidth - 4
		m.worktreeVP.Height = topHeight - 3
		m.judgeVP.Width = judgeWidth - 4
		m.judgeVP.Height = topHeight - 3
		m.claudeVP.Width = m.width - 4
		m.claudeVP.Height = claudeHeight - 3

		return m, nil

	case tea.KeyMsg:
		switch {
		case msg.String() == "ctrl+c":
			if m.coord != nil {
				m.coord.CancelAll()
				m.coord.CleanupAll(context.Background())
			}
			m.cancel()
			return m, tea.Quit
		case msg.String() == "q":
			if m.phase == phaseReview {
				m.cancel()
				return m, tea.Quit
			}
			if m.phase == phaseQualityPrompt {
				// User chose to skip remaining quality fixes
				m.phase = phaseDone
				m.allComplete = true
				m.exitCode = 0
				return m, nil
			}
			if m.confirmQuit || m.phase == phaseDone || m.phase == phaseIdle {
				m.cancel()
				return m, tea.Quit
			}
			m.confirmQuit = true
			return m, nil
		case msg.String() == "tab":
			m.activePanel = (m.activePanel + 1) % panelCount
			return m, nil
		case msg.String() == "j" || msg.String() == "down":
			switch m.activePanel {
			case panelProgress:
				m.progressVP.LineDown(1)
			case panelWorktree:
				m.worktreeVP.LineDown(1)
			case panelJudge:
				m.judgeVP.LineDown(1)
			case panelClaude:
				m.claudeVP.LineDown(1)
			}
			return m, nil
		case msg.String() == "k" || msg.String() == "up":
			switch m.activePanel {
			case panelProgress:
				m.progressVP.LineUp(1)
			case panelWorktree:
				m.worktreeVP.LineUp(1)
			case panelJudge:
				m.judgeVP.LineUp(1)
			case panelClaude:
				m.claudeVP.LineUp(1)
			}
			return m, nil
		case msg.String() == "pgdown":
			switch m.activePanel {
			case panelProgress:
				m.progressVP.ViewDown()
			case panelWorktree:
				m.worktreeVP.ViewDown()
			case panelJudge:
				m.judgeVP.ViewDown()
			case panelClaude:
				m.claudeVP.ViewDown()
			}
			return m, nil
		case msg.String() == "pgup":
			switch m.activePanel {
			case panelProgress:
				m.progressVP.ViewUp()
			case panelWorktree:
				m.worktreeVP.ViewUp()
			case panelJudge:
				m.judgeVP.ViewUp()
			case panelClaude:
				m.claudeVP.ViewUp()
			}
			return m, nil
		case msg.String() == "enter":
			if m.phase == phaseReview {
				m.phase = phaseInit
				m.claudeContent = ""
				m.prevClaudeLen = 0
				return m, archiveCmd(m.cfg)
			}
			if m.phase == phaseQualityPrompt {
				// User chose to continue fixing
				m.qualityIteration++
				m.phase = phaseQualityReview
				m.claudeContent += fmt.Sprintf("\n── Continuing quality review (iteration %d)... ──\n", m.qualityIteration)
				m.claudeVP.SetContent(m.claudeContent)
				m.claudeVP.GotoBottom()
				m.prevClaudeLen = len(m.claudeContent)
				return m, qualityReviewCmd(m.ctx, m.cfg, m.qualityIteration)
			}
		default:
			m.confirmQuit = false
			// Worker tab switching: 1-9
			if m.phase == phaseParallel && len(msg.String()) == 1 && msg.String()[0] >= '1' && msg.String()[0] <= '9' {
				m.activeWorkerView = worker.WorkerID(msg.String()[0] - '0')
				m.claudeContent = ""
				m.prevClaudeLen = 0
			}
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	// --- Fast tick: poll activity + progress ---
	case fastTickMsg:
		cmds = append(cmds, fastTickCmd())
		cmds = append(cmds, pollProgressCmd(m.cfg.ProgressFile))

		// Update spring-animated progress bar
		target := 0.0
		if m.totalStories > 0 {
			target = float64(m.completedStories) / float64(m.totalStories)
		}
		m.animatedFill, m.fillVelocity = m.progressSpring.Update(
			m.animatedFill, m.fillVelocity, target,
		)
		if m.phase == phasePlanning {
			activityPath := filepath.Join(m.cfg.LogDir, "plan-activity.log")
			cmds = append(cmds, pollActivityCmd(activityPath))
		}
		if m.phase == phaseClaudeRun || m.phase == phaseJudgeRun {
			activityPath := runner.ActivityFilePath(m.cfg.LogDir, m.iteration)
			cmds = append(cmds, pollActivityCmd(activityPath))
		}
		if m.phase == phaseClaudeRun {
			cmds = append(cmds, pollStuckCmd(m.cfg.ProjectDir, m.iteration))
		}
		if m.phase == phaseQualityFix {
			activityPath := filepath.Join(m.cfg.LogDir, fmt.Sprintf("quality-fix-%d-activity.log", m.qualityIteration))
			cmds = append(cmds, pollActivityCmd(activityPath))
		}
		if m.phase == phaseParallel && m.coord != nil && m.activeWorkerView > 0 {
			activityPath := m.coord.GetWorkerActivityPath(m.activeWorkerView)
			if activityPath != "" {
				wID := m.activeWorkerView
				cmds = append(cmds, pollWorkerActivityCmd(wID, activityPath))
			}
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

	case claudeActivityMsg:
		m.claudeContent = msg.Content
		newLen := len(msg.Content)
		m.claudeVP.SetContent(msg.Content)
		if newLen > m.prevClaudeLen {
			m.claudeVP.GotoBottom()
		}
		m.prevClaudeLen = newLen

	case prdReloadedMsg:
		m.completedStories = msg.CompletedCount
		m.totalStories = msg.TotalCount

	// --- Phase transitions ---
	case planDoneMsg:
		if msg.Err != nil {
			m.claudeContent += fmt.Sprintf("\n── Plan Error ──\n%s\n", msg.Err)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			m.phase = phaseDone
			m.exitCode = 1
			return m, nil
		}
		m.phase = phaseReview
		m.claudeContent += "\n── prd.json generated. Review it, then press Enter to execute (q to quit) ──\n"
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)
		return m, nil

	case archiveDoneMsg:
		if m.cfg.Workers > 1 {
			m.phase = phaseDagAnalysis
			cmds = append(cmds, dagAnalyzeCmd(m.ctx, m.cfg))
		} else {
			m.phase = phaseIterating
			cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
		}

	case nextStoryMsg:
		if msg.AllDone {
			return m.transitionToComplete()
		}
		m.iteration++
		if m.iteration > m.cfg.MaxIterations {
			m.phase = phaseDone
			m.allComplete = false
			m.exitCode = 1
			return m, nil
		}
		m.currentStoryID = msg.StoryID
		m.currentStoryTitle = msg.StoryTitle
		m.phase = phaseClaudeRun
		m.claudeContent = ""
		m.prevClaudeLen = 0

		// Capture revision for judge diff baseline
		if m.cfg.JudgeEnabled {
			dirs := []string{m.cfg.ProjectDir}
			if p, err := prd.Load(m.cfg.PRDFile); err == nil {
				for _, r := range p.Repos {
					if filepath.IsAbs(r) {
						dirs = append(dirs, r)
					} else {
						dirs = append(dirs, filepath.Join(m.cfg.ProjectDir, r))
					}
				}
			}
			m.preRevs = captureRevsCmd(m.ctx, dirs)
		}

		cmds = append(cmds, runClaudeCmd(m.ctx, m.cfg, msg.StoryID, m.iteration))

	case claudeDoneMsg:
		if msg.Err != nil {
			// Context cancelled = user quit
			if m.ctx.Err() != nil {
				return m, tea.Quit
			}
			// Show Claude error in activity panel
			m.claudeContent += fmt.Sprintf("\n── Claude Error ──\n%s\n", msg.Err)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
		}

		if msg.CompleteSignal {
			_ = events.Append(m.cfg.ProjectDir, events.Event{
				Type:    events.EventStoryComplete,
				StoryID: m.currentStoryID,
				Summary: "All stories complete (COMPLETE signal received)",
			})
			return m.transitionToComplete()
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

	case stuckDetectedMsg:
		// Cancel Claude — it's stuck
		m.cancel()
		// Recreate context for future operations
		m.ctx, m.cancel = context.WithCancel(context.Background())

		_ = events.Append(m.cfg.ProjectDir, events.Event{
			Type:    events.EventStuck,
			StoryID: msg.Info.StoryID,
			Summary: fmt.Sprintf("Stuck: %s (%dx)", msg.Info.Pattern, msg.Info.Count),
			Errors:  msg.Info.Commands,
			Meta:    map[string]string{"iteration": fmt.Sprintf("%d", msg.Info.Iteration)},
		})

		// Append [STUCK] to progress
		if f, err := os.OpenFile(m.cfg.ProgressFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
			fmt.Fprintf(f, "\n## [%s] %s [STUCK]\n- Pattern: %s (%dx)\n- Commands: %s\n---\n",
				time.Now().Format("2006-01-02 15:04"), msg.Info.StoryID, msg.Info.Pattern, msg.Info.Count,
				strings.Join(msg.Info.Commands, ", "))
			f.Close()
		}

		m.claudeContent += fmt.Sprintf("\n── STUCK DETECTED: %s (%dx) ──\n", msg.Info.Pattern, msg.Info.Count)
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)

		// If this is a FIX- story, mark as failed and move on
		if strings.HasPrefix(m.currentStoryID, "FIX-") {
			if p, err := prd.Load(m.cfg.PRDFile); err == nil {
				p.SetPasses(m.currentStoryID, false)
				_ = prd.Save(m.cfg.PRDFile, p)
			}
			m.phase = phaseIterating
			cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
		} else {
			// Generate fix story if one doesn't already exist
			fixID := "FIX-" + m.currentStoryID
			if p, err := prd.Load(m.cfg.PRDFile); err == nil && !p.HasStory(fixID) {
				cmds = append(cmds, generateFixStoryCmd(m.ctx, m.cfg, msg.Info))
			} else {
				m.phase = phaseIterating
				cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
			}
		}

	case fixStoryGeneratedMsg:
		if msg.Err != nil {
			m.claudeContent += fmt.Sprintf("\n── Fix story generation failed: %s ──\n", msg.Err)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
		} else if msg.StoryID != "" {
			m.claudeContent += fmt.Sprintf("\n── Fix story generated: %s ──\n", msg.StoryID)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
		}
		m.phase = phaseIterating
		cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))

	// --- Parallel execution messages ---
	case coordinator.DAGAnalyzedMsg:
		if msg.Err != nil || msg.DAG == nil {
			// Fallback to serial
			m.claudeContent += fmt.Sprintf("\n── DAG analysis failed: %v — falling back to serial ──\n", msg.Err)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			m.cfg.Workers = 1
			m.phase = phaseIterating
			cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
		} else {
			m.storyDAG = msg.DAG
			p, err := prd.Load(m.cfg.PRDFile)
			if err != nil {
				m.phase = phaseDone
				m.exitCode = 1
				return m, nil
			}
			// Filter to incomplete stories only
			var incomplete []prd.UserStory
			for _, s := range p.UserStories {
				if !s.Passes {
					incomplete = append(incomplete, s)
				}
			}
			m.coord = coordinator.New(m.cfg, m.storyDAG, m.cfg.Workers, incomplete)
			m.phase = phaseParallel
			m.coord.ScheduleReady(m.ctx)
			cmds = append(cmds, m.coord.ListenCmd())
		}

	case coordinator.WorkerUpdateMsg:
		u := msg.Update
		willRetry := m.coord.HandleUpdate(u)

		// Update active worker view to first active worker if current is gone
		if m.activeWorkerView == 0 || u.WorkerID == m.activeWorkerView {
			m.activeWorkerView = u.WorkerID
		}

		switch u.State {
		case worker.WorkerDone:
			if u.Passed && u.ChangeID != "" {
				cmds = append(cmds, mergeBackCmd(m.ctx, m.coord, u))
			} else {
				// Failed or didn't pass — cleanup workspace
				go m.coord.CleanupWorker(m.ctx, u.WorkerID)
				// Try to schedule more
				m.coord.ScheduleReady(m.ctx)
				if m.coord.AllDone() {
					if m.coord.CompletedCount() == m.totalStories {
						return m.transitionToComplete()
					}
					m.phase = phaseDone
					m.allComplete = false
					m.exitCode = 1
					return m, nil
				}
			}
		case worker.WorkerFailed:
			go m.coord.CleanupWorker(m.ctx, u.WorkerID)
			if willRetry {
				m.claudeContent += fmt.Sprintf("\n── Worker %d failed (%s): %v — retrying ──\n", u.WorkerID, u.StoryID, u.Err)
			} else {
				m.claudeContent += fmt.Sprintf("\n── Worker %d failed (%s): %v ──\n", u.WorkerID, u.StoryID, u.Err)
			}
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			m.coord.ScheduleReady(m.ctx)
			if m.coord.AllDone() {
				m.phase = phaseDone
				m.exitCode = 1
				return m, nil
			}
		}
		// Keep listening for more updates
		cmds = append(cmds, m.coord.ListenCmd())

	case coordinator.MergeCompleteMsg:
		if msg.Err != nil {
			m.claudeContent += fmt.Sprintf("\n── Merge failed (%s): %v ──\n", msg.StoryID, msg.Err)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
		} else {
			m.claudeContent += fmt.Sprintf("\n── Merged %s into main ──\n", msg.StoryID)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			// Update story counts immediately (don't wait for slow tick)
			m.completedStories = m.coord.CompletedCount()
		}
		go m.coord.CleanupWorker(m.ctx, msg.WorkerID)
		// Schedule more work
		m.coord.ScheduleReady(m.ctx)
		if m.coord.AllDone() {
			// Final sync of story counts
			m.completedStories = m.coord.CompletedCount()
			if m.completedStories == m.totalStories {
				return m.transitionToComplete()
			}
			m.phase = phaseDone
			m.allComplete = false
			m.exitCode = 1
			return m, nil
		}

	case coordinator.WorkerActivityMsg:
		if msg.WorkerID == m.activeWorkerView {
			m.claudeContent = msg.Content
			newLen := len(msg.Content)
			m.claudeVP.SetContent(msg.Content)
			if newLen > m.prevClaudeLen {
				m.claudeVP.GotoBottom()
			}
			m.prevClaudeLen = newLen
		}

	case judgeDoneMsg:
		// Show judge result in the judge panel
		m.judgeContent += judge.FormatResult(m.currentStoryID, msg.Result)
		newJudgeLen := len(m.judgeContent)
		m.judgeVP.SetContent(m.judgeContent)
		if newJudgeLen > m.prevJudgeLen {
			m.judgeVP.GotoBottom()
		}
		m.prevJudgeLen = newJudgeLen

		// Persist judge result to progress.txt
		judge.AppendJudgeResult(m.cfg.ProgressFile, m.currentStoryID, msg.Result)

		if msg.Result.Passed {
			judge.ClearRejectionCount(m.cfg.ProjectDir, m.currentStoryID)
			_ = events.Append(m.cfg.ProjectDir, events.Event{
				Type:    events.EventJudgeResult,
				StoryID: m.currentStoryID,
				Summary: "Judge passed: " + msg.Result.Reason,
				Meta:    map[string]string{"verdict": "pass"},
			})
		} else {
			judge.IncrementRejectionCount(m.cfg.ProjectDir, m.currentStoryID)
			_ = events.Append(m.cfg.ProjectDir, events.Event{
				Type:    events.EventJudgeResult,
				StoryID: m.currentStoryID,
				Summary: "Judge failed: " + msg.Result.Reason,
				Errors:  msg.Result.CriteriaFailed,
				Meta:    map[string]string{"verdict": "fail"},
			})
		}
		// Either way, move to next iteration
		m.phase = phaseIterating
		cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))

	// --- Quality review messages ---
	case qualityReviewDoneMsg:
		if msg.Err != nil {
			m.claudeContent += fmt.Sprintf("\n── Quality review error: %v ──\n", msg.Err)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			m.phase = phaseDone
			m.allComplete = true
			m.exitCode = 0
			return m, nil
		}

		m.lastAssessment = &msg.Assessment
		summary := quality.FormatSummary(msg.Assessment)
		m.claudeContent += "\n" + summary
		m.judgeContent += "\n" + summary
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)
		m.judgeVP.SetContent(m.judgeContent)
		m.judgeVP.GotoBottom()
		m.prevJudgeLen = len(m.judgeContent)

		if msg.Assessment.TotalFindings() == 0 {
			m.claudeContent += "\n── Quality review: all clean! ──\n"
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			m.phase = phaseDone
			m.allComplete = true
			m.exitCode = 0
			return m, nil
		}

		// Has findings — start fix phase
		m.phase = phaseQualityFix
		m.claudeContent += "\n── Fixing quality issues... ──\n"
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)
		cmds = append(cmds, qualityFixCmd(m.ctx, m.cfg, msg.Assessment, m.qualityIteration))

	case qualityFixDoneMsg:
		if msg.Err != nil {
			m.claudeContent += fmt.Sprintf("\n── Quality fix error: %v ──\n", msg.Err)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
		}

		if m.qualityIteration >= m.cfg.QualityMaxIters {
			// Max iterations reached — prompt user
			m.phase = phaseQualityPrompt
			m.claudeContent += "\n── Max quality iterations reached. Press Enter to continue fixing, q to finish ──\n"
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			return m, nil
		}

		// Re-review
		m.qualityIteration++
		m.phase = phaseQualityReview
		m.claudeContent += fmt.Sprintf("\n── Re-reviewing (iteration %d)... ──\n", m.qualityIteration)
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)
		cmds = append(cmds, qualityReviewCmd(m.ctx, m.cfg, m.qualityIteration))
	}

	return m, tea.Batch(cmds...)
}

// transitionToComplete handles the "all stories done" transition.
// If quality review is enabled and hasn't run yet, starts quality review.
// Otherwise, transitions to phaseDone.
func (m *Model) transitionToComplete() (tea.Model, tea.Cmd) {
	if m.cfg.QualityReview && m.qualityIteration == 0 {
		m.qualityIteration = 1
		m.phase = phaseQualityReview
		m.claudeContent = "── Starting quality review ──\n"
		m.claudeVP.SetContent(m.claudeContent)
		m.prevClaudeLen = len(m.claudeContent)
		return m, qualityReviewCmd(m.ctx, m.cfg, m.qualityIteration)
	}
	m.phase = phaseDone
	m.allComplete = true
	m.exitCode = 0
	return m, nil
}

func (m *Model) handleJudgeCheck() tea.Cmd {
	// Skip judge if no pre-revisions were captured
	if len(m.preRevs) == 0 {
		return nil
	}

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
		m.judgeContent += fmt.Sprintf("\n── Judge: %s ── AUTO-PASS after %d rejections [HUMAN REVIEW NEEDED] ──\n", m.currentStoryID, rejections)
		m.judgeVP.SetContent(m.judgeContent)
		m.judgeVP.GotoBottom()
		m.prevJudgeLen = len(m.judgeContent)
		return nil
	}

	m.phase = phaseJudgeRun
	m.judgeContent += "\n── Judge reviewing " + m.currentStoryID + "... ──\n"
	m.judgeVP.SetContent(m.judgeContent)
	m.judgeVP.GotoBottom()
	m.prevJudgeLen = len(m.judgeContent)
	return runJudgeCmd(m.ctx, m.cfg, m.currentStoryID, m.preRevs)
}

func (m *Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	// Layout: header(3) + top panels + claude activity + footer(1)
	// Reserve exact line counts for fixed elements
	headerHeight := 3
	footerHeight := 1
	available := m.height - headerHeight - footerHeight
	if available < 10 {
		available = 10
	}

	// Split: 35% top panels, 65% claude activity
	topHeight := available * 35 / 100
	if topHeight < 5 {
		topHeight = 5
	}
	claudeHeight := available - topHeight

	progressWidth := m.width * 40 / 100
	worktreeWidth := m.width * 30 / 100
	judgeWidth := m.width - progressWidth - worktreeWidth

	// Render sections
	header := renderHeader(m, m.width)

	progressPanel := renderProgressPanel(
		m.progressVP,
		m.activePanel == panelProgress,
		progressWidth,
		topHeight,
	)

	worktreePanel := renderWorktreePanel(
		m.worktreeVP,
		m.worktreeContent,
		m.activePanel == panelWorktree,
		worktreeWidth,
		topHeight,
	)

	judgePanel := renderJudgePanel(
		m.judgeVP,
		m.judgeContent,
		m.activePanel == panelJudge,
		judgeWidth,
		topHeight,
	)

	topRow := lipgloss.JoinHorizontal(lipgloss.Top, progressPanel, worktreePanel, judgePanel)

	claudeRunning := m.phase == phaseClaudeRun || m.phase == phaseJudgeRun || m.phase == phaseParallel || m.phase == phaseDagAnalysis || m.phase == phasePlanning || m.phase == phaseQualityReview || m.phase == phaseQualityFix
	var workerTabStr string
	if m.phase == phaseParallel && m.coord != nil {
		workers := m.coord.Workers()
		var tabParts []string
		for id, w := range workers {
			if w.State == worker.WorkerIdle {
				continue
			}
			marker := ""
			if id == m.activeWorkerView {
				marker = ">"
			}
			tabParts = append(tabParts, fmt.Sprintf("%s%d:%s[%s]", marker, id, w.StoryID, w.State))
		}
		workerTabStr = strings.Join(tabParts, " | ")
	}
	claudePanel := renderClaudePanel(
		m.claudeVP,
		m.spinner,
		m.claudeContent,
		claudeRunning,
		m.activePanel == panelClaude,
		m.width,
		claudeHeight,
		workerTabStr,
	)

	footer := renderFooter(m.width, m.confirmQuit, m.phase == phaseDone, m.phase == phaseIdle, m.phase == phaseParallel, m.phase == phaseReview, m.phase == phaseQualityPrompt)

	output := lipgloss.JoinVertical(lipgloss.Left,
		header,
		topRow,
		claudePanel,
		footer,
	)

	// Clamp to exactly terminal height to prevent scrolling/jitter
	lines := strings.Split(output, "\n")
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// clampLines truncates or pads a string to exactly n lines.
func clampLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func renderFooter(width int, confirmQuit bool, done bool, idle bool, parallel bool, review bool, qualityPrompt bool) string {
	if confirmQuit {
		return "  " + styleQuitConfirm.Render("Press q again to quit, any other key to cancel")
	}
	help := styleKey.Render("q") + styleFooter.Render(": quit  ") +
		styleKey.Render("tab") + styleFooter.Render(": switch panel  ") +
		styleKey.Render("j/k") + styleFooter.Render(": scroll")
	if parallel {
		help += "  " + styleKey.Render("1-9") + styleFooter.Render(": switch worker")
	}
	if qualityPrompt {
		qpHelp := styleKey.Render("enter") + styleFooter.Render(": continue fixing  ") +
			styleKey.Render("q") + styleFooter.Render(": finish  ") +
			styleKey.Render("tab") + styleFooter.Render(": switch panel  ") +
			styleKey.Render("j/k") + styleFooter.Render(": scroll")
		return "  " + qpHelp
	}
	if review {
		reviewHelp := styleKey.Render("enter") + styleFooter.Render(": execute  ") +
			styleKey.Render("q") + styleFooter.Render(": quit  ") +
			styleKey.Render("tab") + styleFooter.Render(": switch panel  ") +
			styleKey.Render("j/k") + styleFooter.Render(": scroll")
		return "  " + reviewHelp
	}
	if idle {
		return "  " + styleMuted.Render("Idle — ") + help
	}
	if done {
		return "  " + styleSuccess.Render("Run complete — ") + help
	}
	return "  " + help
}
