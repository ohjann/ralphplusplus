package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"
	"github.com/eoghanhynes/ralph/internal/checkpoint"
	"github.com/eoghanhynes/ralph/internal/config"
	"github.com/eoghanhynes/ralph/internal/memory"
	"github.com/eoghanhynes/ralph/internal/coordinator"
	"github.com/eoghanhynes/ralph/internal/dag"
	"github.com/eoghanhynes/ralph/internal/debuglog"
	"github.com/eoghanhynes/ralph/internal/events"
	"github.com/eoghanhynes/ralph/internal/judge"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/quality"
	"github.com/eoghanhynes/ralph/internal/runner"
	"github.com/eoghanhynes/ralph/internal/storystate"
	"github.com/eoghanhynes/ralph/internal/worker"
	"github.com/eoghanhynes/ralph/internal/workspace"
)

const (
	panelStories = iota
	panelContext
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
	completionReason string // why the run finished
	startTime        time.Time
	confirmQuit      bool
	pausedDuring     phase // which phase we were in when paused (for resume)

	// Panel content
	progressContent string
	progressChanged bool
	worktreeContent string
	claudeContent   string
	judgeContent    string
	qualityContent  string

	// Story data for the stories panel
	storyDisplayInfos []StoryDisplayInfo
	animFrame         int // animation frame for spinners

	// Context panel mode
	ctxMode          contextMode
	ctxModeManual    bool  // true when user manually switched tabs; suppresses auto-select
	ctxManualAtPhase phase // phase when manual override was set; resets on phase change

	// Active panel for scrolling
	activePanel int

	// Components
	storiesVP  viewport.Model
	contextVP  viewport.Model
	claudeVP   viewport.Model
	spinner    spinner.Model

	// Terminal size
	width  int
	height int

	// Track if we should auto-scroll
	prevContextLen int
	prevClaudeLen  int

	// Spring-animated progress bar
	progressSpring harmonica.Spring
	animatedFill   float64 // current animated fill ratio (0.0–1.0)
	fillVelocity   float64 // spring velocity

	// Parallel execution
	coord            *coordinator.Coordinator
	storyDAG         *dag.DAG
	activeWorkerView worker.WorkerID // which worker's output to show in Claude panel

	// Checkpoint
	prdHash string

	// Quality review
	qualityIteration  int
	lastAssessment    *quality.Assessment

	// Worker log cache: persists logs after workspace cleanup
	workerLogCache map[worker.WorkerID]string
	// Ordered list of worker tab entries for stable 1-9 mapping
	workerTabOrder []worker.WorkerID

	// Resume checkpoint (loaded during phaseInit)
	loadedCheckpoint *checkpoint.Checkpoint

	// Memory / ChromaDB sidecar
	memorySidecar  *memory.Sidecar
	chromaClient   *memory.ChromaClient
	memoryEmbedder memory.Embedder
	memoryContent  string // rendered content for the memory context panel tab
	confirmTracker *memory.ConfirmationTracker
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
		storiesVP:      newStoriesViewport(35, 10),
		contextVP:      newContextViewport(60, 10),
		claudeVP:       newClaudeViewport(80, 20),
		progressSpring: harmonica.NewSpring(harmonica.FPS(30), 6.0, 0.5),
		workerLogCache: make(map[worker.WorkerID]string),
		confirmTracker: memory.NewConfirmationTracker(),
	}
}

func (m *Model) ExitCode() int {
	return m.exitCode
}

// stopSidecar cleanly shuts down the ChromaDB sidecar if running.
func (m *Model) stopSidecar() {
	if m.memorySidecar != nil {
		if err := m.memorySidecar.Stop(); err != nil {
			debuglog.Log("chromadb sidecar stop error: %v", err)
		}
	}
}

// contextContentWidth returns the usable content width for the context panel.
func (m *Model) contextContentWidth() int {
	storiesWidth := m.width * 35 / 100
	contextWidth := m.width - storiesWidth
	return max(contextWidth-4, 0)
}

func (m *Model) Init() tea.Cmd {
	setTitle := tea.SetWindowTitle("✦ ralph")

	if m.cfg.IdleMode {
		m.phase = phaseIdle
		return tea.Batch(
			setTitle,
			m.spinner.Tick,
			fastTickCmd(),
			tickCmd(),
		)
	}
	if m.cfg.PlanFile != "" {
		m.phase = phasePlanning
		return tea.Batch(
			setTitle,
			planCmd(m.ctx, m.cfg),
			m.spinner.Tick,
			fastTickCmd(),
			tickCmd(),
		)
	}
	return tea.Batch(
		setTitle,
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

		storiesWidth := m.width * 35 / 100
		contextWidth := m.width - storiesWidth

		m.storiesVP.Width = storiesWidth - 4
		m.storiesVP.Height = topHeight - 3
		m.contextVP.Width = contextWidth - 4
		m.contextVP.Height = topHeight - 4 // extra line for tab bar
		m.claudeVP.Width = m.width - 4
		m.claudeVP.Height = claudeHeight - 3

		// Re-render markdown at new width if we have content.
		if m.progressContent != "" {
			contentW := m.contextContentWidth()
			if cmd := maybeRenderMarkdown(m.progressContent, contentW); cmd != nil {
				return m, cmd
			}
		}
		return m, nil

	case tea.KeyMsg:
		switch {
		case msg.String() == "ctrl+c":
			if m.coord != nil {
				m.coord.CancelAll()
				m.coord.CleanupAll(context.Background())
			}
			m.stopSidecar()
			m.cancel()
			return m, tea.Quit
		case msg.String() == "y":
			if m.phase == phaseResumePrompt {
				cp := m.loadedCheckpoint
				m.iteration = cp.IterationCount
				m.completedStories = len(cp.CompletedStories)

				if cp.Phase == "parallel" && m.cfg.Workers > 1 {
					p, err := prd.Load(m.cfg.PRDFile)
					if err != nil {
						debuglog.Log("Error loading PRD for resume: %v", err)
						m.phase = phaseIterating
						cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
						return m, tea.Batch(cmds...)
					}

					m.storyDAG = dag.FromCheckpoint(cp.DAG, p.UserStories)

					var incomplete []prd.UserStory
					for _, s := range p.UserStories {
						if !s.Passes {
							incomplete = append(incomplete, s)
						}
					}

					m.coord = coordinator.NewFromCheckpoint(
						m.cfg, m.storyDAG, m.cfg.Workers, incomplete,
						cp.CompletedStories, cp.FailedStories, cp.IterationCount,
					)
					m.phase = phaseParallel
					m.coord.ScheduleReady(m.ctx)
					cmds = append(cmds, m.coord.ListenCmd())
				} else {
					m.phase = phaseIterating
					cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
				}
				return m, tea.Batch(cmds...)
			}
		case msg.String() == "n":
			if m.phase == phaseResumePrompt {
				// Start fresh — delete checkpoint and continue normal startup
				_ = checkpoint.Delete(m.cfg.ProjectDir)
				m.loadedCheckpoint = nil
				if m.cfg.Workers > 1 {
					m.phase = phaseDagAnalysis
					cmds = append(cmds, dagAnalyzeCmd(m.ctx, m.cfg))
				} else {
					m.phase = phaseIterating
					cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
				}
				return m, tea.Batch(cmds...)
			}
		case msg.String() == "q":
			if m.phase == phaseResumePrompt {
				m.stopSidecar()
				m.cancel()
				return m, tea.Quit
			}
			if m.phase == phaseReview {
				m.stopSidecar()
				m.cancel()
				return m, tea.Quit
			}
			if m.phase == phaseQualityPrompt {
				// User chose to skip remaining quality fixes
				return m.transitionToSummary()
			}
			if m.confirmQuit || m.phase == phaseDone || m.phase == phaseIdle {
				m.stopSidecar()
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
			case panelStories:
				m.storiesVP.LineDown(1)
			case panelContext:
				m.contextVP.LineDown(1)
			case panelClaude:
				m.claudeVP.LineDown(1)
			}
			return m, nil
		case msg.String() == "k" || msg.String() == "up":
			switch m.activePanel {
			case panelStories:
				m.storiesVP.LineUp(1)
			case panelContext:
				m.contextVP.LineUp(1)
			case panelClaude:
				m.claudeVP.LineUp(1)
			}
			return m, nil
		case msg.String() == "pgdown":
			switch m.activePanel {
			case panelStories:
				m.storiesVP.ViewDown()
			case panelContext:
				m.contextVP.ViewDown()
			case panelClaude:
				m.claudeVP.ViewDown()
			}
			return m, nil
		case msg.String() == "pgup":
			switch m.activePanel {
			case panelStories:
				m.storiesVP.ViewUp()
			case panelContext:
				m.contextVP.ViewUp()
			case panelClaude:
				m.claudeVP.ViewUp()
			}
			return m, nil
		case msg.String() == "[" || msg.String() == "]":
			// Cycle context panel tabs
			if msg.String() == "]" {
				m.ctxMode = (m.ctxMode + 1) % contextModeCount
			} else {
				m.ctxMode = (m.ctxMode + contextModeCount - 1) % contextModeCount // -1 with wrap
			}
			m.ctxModeManual = true
			m.ctxManualAtPhase = m.phase
			return m, nil
		case msg.String() == "enter":
			if m.phase == phasePaused {
				m.claudeContent += "\n── Resuming... ──\n"
				m.claudeVP.SetContent(m.claudeContent)
				m.claudeVP.GotoBottom()
				m.prevClaudeLen = len(m.claudeContent)
				m.ctx, m.cancel = context.WithCancel(context.Background())

				switch m.pausedDuring {
				case phaseParallel:
					m.coord.Resume()
					m.phase = phaseParallel
					m.coord.ScheduleReady(m.ctx)
					return m, tea.Batch(m.coord.ListenCmd(), fastTickCmd(), tickCmd())
				case phasePlanning:
					m.phase = phasePlanning
					return m, tea.Batch(planCmd(m.ctx, m.cfg), fastTickCmd())
				default:
					// Serial mode — retry current story
					m.phase = phaseIterating
					return m, findNextStoryCmd(m.cfg.PRDFile)
				}
			}
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
			// Worker tab switching: 1-9 maps to tab position, not worker ID
			if m.phase == phaseParallel && len(msg.String()) == 1 && msg.String()[0] >= '1' && msg.String()[0] <= '9' {
				idx := int(msg.String()[0]-'0') - 1
				if idx < len(m.workerTabOrder) {
					wID := m.workerTabOrder[idx]
					m.activeWorkerView = wID
					// Load cached logs if available (for completed workers)
					if cached, ok := m.workerLogCache[wID]; ok {
						m.claudeContent = cached
						m.prevClaudeLen = len(cached)
					} else {
						m.claudeContent = ""
						m.prevClaudeLen = 0
					}
				}
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

		// Advance animation frame
		m.animFrame++

		// Update story display infos from PRD
		if p, err := prd.Load(m.cfg.PRDFile); err == nil {
			var coordIface interface {
				Workers() map[worker.WorkerID]*worker.Worker
			}
			if m.coord != nil {
				coordIface = m.coord
			}
			m.storyDisplayInfos = BuildStoryDisplayInfos(p.UserStories, m.currentStoryID, coordIface, m.phase)
		}

		// Auto-select context mode based on phase (skip if user manually switched)
		// Reset manual override when the phase changes so new phases can auto-select.
		if m.ctxModeManual && m.phase != m.ctxManualAtPhase {
			m.ctxModeManual = false
		}
		if !m.ctxModeManual {
			autoMode := autoSelectContextMode(m.phase, m.judgeContent, m.qualityContent)
			if autoMode != m.ctxMode {
				switch m.phase {
				case phaseJudgeRun, phaseQualityReview, phaseQualityFix, phaseQualityPrompt:
					m.ctxMode = autoMode
				}
			}
		}

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
		if m.phase == phaseClaudeRun {
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
		if m.phase == phaseSummary {
			activityPath := filepath.Join(m.cfg.LogDir, "summary-activity.log")
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
		// Refresh memory stats periodically (picks up embedding pipeline changes)
		if m.chromaClient != nil {
			cmds = append(cmds, memoryStatsCmd(m.ctx, m.chromaClient, m.cfg.Memory.Disabled))
		}

	// --- Data updates ---
	case progressContentMsg:
		if msg.Content != m.progressContent {
			m.progressChanged = true
			// Kick off async markdown rendering for the new content.
			contentW := m.contextContentWidth()
			if cmd := maybeRenderMarkdown(msg.Content, contentW); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		m.progressContent = msg.Content

	case markdownRenderedMsg:
		applyMarkdownRendered(msg)

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
			var usageErr *runner.UsageLimitError
			if errors.As(msg.Err, &usageErr) {
				m.pausedDuring = phasePlanning
				m.phase = phasePaused
				m.claudeContent += "\n── Usage Limit Hit ──\nClaude API usage limit reached during planning.\nPress Enter to resume when your limit resets.\n"
				m.claudeVP.SetContent(m.claudeContent)
				m.claudeVP.GotoBottom()
				m.prevClaudeLen = len(m.claudeContent)
				return m, nil
			}
			m.claudeContent += fmt.Sprintf("\n── Plan Error ──\n%s\n", msg.Err)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			m.phase = phaseDone
			m.exitCode = 1
			m.completionReason = fmt.Sprintf("Planning failed: %v", msg.Err)
			debuglog.Log("entering phaseDone: %s", m.completionReason)
			m.showCompletionReport()
			return m, nil
		}
		m.phase = phaseReview
		m.claudeContent += "\n── prd.json generated. Review it, then press Enter to execute (q to quit) ──\n"
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)
		return m, nil

	case archiveDoneMsg:
		// Start ChromaDB sidecar setup in background (unless disabled)
		if !m.cfg.Memory.Disabled {
			cmds = append(cmds, chromaSetupCmd(m.ctx, m.cfg))
		} else {
			m.memoryContent = "  Memory disabled"
		}

		// Check for existing checkpoint to offer resume
		cp, exists, err := checkpoint.Load(m.cfg.ProjectDir)
		if err == nil && exists {
			currentHash, hashErr := checkpoint.ComputePRDHash(m.cfg.PRDFile)
			if hashErr == nil && currentHash == cp.PRDHash {
				// Valid checkpoint — offer resume
				m.loadedCheckpoint = &cp
				m.phase = phaseResumePrompt
				return m, tea.Batch(cmds...)
			}
			// Stale checkpoint — PRD changed, delete and continue
			m.claudeContent += "── Checkpoint found but PRD has changed — starting fresh ──\n"
			m.claudeVP.SetContent(m.claudeContent)
			m.prevClaudeLen = len(m.claudeContent)
			_ = checkpoint.Delete(m.cfg.ProjectDir)
		}
		// Compute PRD hash for checkpointing
		if hash, err := checkpoint.ComputePRDHash(m.cfg.PRDFile); err == nil {
			m.prdHash = hash
		}
		if m.cfg.Workers > 1 {
			m.phase = phaseDagAnalysis
			cmds = append(cmds, dagAnalyzeCmd(m.ctx, m.cfg))
		} else {
			m.phase = phaseIterating
			cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
		}

	case chromaSetupDoneMsg:
		if msg.Err != nil {
			debuglog.Log("chromadb setup failed (degrading gracefully): %v", msg.Err)
			m.memoryContent = "  Memory unavailable (ChromaDB not running)"
		} else {
			m.memorySidecar = msg.Sidecar
			m.chromaClient = msg.Client
			m.confirmTracker = memory.NewConfirmationTracker()
			// Create embedder for semantic memory retrieval in BuildPrompt
			if embedder, err := memory.NewAnthropicEmbedder(); err != nil {
				debuglog.Log("memory embedder init failed (retrieval degraded): %v", err)
			} else {
				m.memoryEmbedder = embedder
			}
			// Trigger codebase scan in background now that sidecar is healthy
			if m.memoryEmbedder != nil {
				cmds = append(cmds, codebaseScanCmd(m.ctx, m.cfg, m.chromaClient, m.memoryEmbedder))
			}
			cmds = append(cmds, memoryStatsCmd(m.ctx, m.chromaClient, m.cfg.Memory.Disabled))
		}

	case codebaseScanDoneMsg:
		if msg.Err != nil {
			debuglog.Log("codebase scan failed (non-fatal): %v", msg.Err)
		}
		// Refresh memory stats after scan completes (collection counts changed)
		if m.chromaClient != nil {
			cmds = append(cmds, memoryStatsCmd(m.ctx, m.chromaClient, m.cfg.Memory.Disabled))
		}

	case memoryStatsMsg:
		m.memoryContent = msg.Content

	case pipelineEmbedDoneMsg:
		if msg.Err != nil {
			debuglog.Log("pipeline embed failed for %s (non-fatal): %v", msg.StoryID, msg.Err)
		} else {
			debuglog.Log("pipeline embed complete for %s", msg.StoryID)
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
			m.completionReason = fmt.Sprintf("Max iterations reached (%d)", m.cfg.MaxIterations)
			debuglog.Log("entering phaseDone: %s", m.completionReason)
			m.showCompletionReport()
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

		cmds = append(cmds, runClaudeCmd(m.ctx, m.cfg, msg.StoryID, m.iteration, m.chromaClient, m.memoryEmbedder))

	case claudeDoneMsg:
		debuglog.Log("claudeDone: story=%s err=%v completeSignal=%v", m.currentStoryID, msg.Err, msg.CompleteSignal)
		if msg.Err != nil {
			// Context cancelled = user quit
			if m.ctx.Err() != nil {
				debuglog.Log("claudeDone: context cancelled, quitting")
				m.stopSidecar()
				return m, tea.Quit
			}
			// Usage limit — pause and wait for user
			var usageErr *runner.UsageLimitError
			if errors.As(msg.Err, &usageErr) {
				m.pausedDuring = phaseClaudeRun
				m.phase = phasePaused
				m.claudeContent += "\n── Usage Limit Hit ──\nClaude API usage limit reached.\nPress Enter to resume when your limit resets.\n"
				m.claudeVP.SetContent(m.claudeContent)
				m.claudeVP.GotoBottom()
				m.prevClaudeLen = len(m.claudeContent)
				return m, nil
			}

			// Show Claude error in activity panel
			m.claudeContent += fmt.Sprintf("\n── Claude Error ──\n%s\n", msg.Err)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
		}

		// Mark current story as passed in prd.json if agent reported it complete.
		// The system owns the passes field — the agent no longer modifies prd.json.
		if msg.Err == nil && m.currentStoryID != "" {
			ss, _ := storystate.Load(m.cfg.ProjectDir, m.currentStoryID)
			if ss.Status == storystate.StatusComplete {
				if p, err := prd.Load(m.cfg.PRDFile); err == nil {
					p.SetPasses(m.currentStoryID, true)
					_ = prd.Save(m.cfg.PRDFile, p)
				}
				// Confirm retrieved docs that contributed to this successful story
				if m.confirmTracker != nil && len(msg.DocRefs) > 0 {
					for _, ref := range msg.DocRefs {
						_ = m.confirmTracker.ConfirmDocument(m.ctx, ref.Collection, ref.DocID)
					}
					debuglog.Log("memory: confirmed %d doc refs for story %s", len(msg.DocRefs), m.currentStoryID)
				}
				// Trigger embedding pipeline for completed story
				if m.chromaClient != nil && m.memoryEmbedder != nil {
					cmds = append(cmds, runPipelineCmd(m.ctx, m.chromaClient, m.memoryEmbedder, m.cfg.ProjectDir, m.currentStoryID, false))
				}
			} else if ss.Status == storystate.StatusContextExhausted {
				// Trigger embedding pipeline for context-exhausted story
				if m.chromaClient != nil && m.memoryEmbedder != nil {
					cmds = append(cmds, runPipelineCmd(m.ctx, m.chromaClient, m.memoryEmbedder, m.cfg.ProjectDir, m.currentStoryID, true))
				}
			}
		}

		// Write checkpoint after each serial iteration
		m.writeSerialCheckpoint(msg.Err)

		if msg.CompleteSignal {
			debuglog.Log("claudeDone: COMPLETE signal received during story=%s, transitioning to complete", m.currentStoryID)
			_ = events.Append(m.cfg.ProjectDir, events.Event{
				Type:    events.EventStoryComplete,
				StoryID: m.currentStoryID,
				Summary: "All stories complete (COMPLETE signal received)",
			})
			return m.transitionToComplete()
		}

		// Judge check
		if m.cfg.JudgeEnabled && m.currentStoryID != "" {
			if judgeCmd := m.handleJudgeCheck(); judgeCmd != nil {
				cmds = append(cmds, judgeCmd)
				return m, tea.Batch(cmds...)
			}
		}

		// No judge or story didn't pass yet — next iteration
		m.phase = phaseIterating
		cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))

	case stuckDetectedMsg:
		debuglog.Log("stuck detected: story=%s pattern=%s count=%d", msg.Info.StoryID, msg.Info.Pattern, msg.Info.Count)
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
				m.completionReason = fmt.Sprintf("Failed to load prd.json for parallel execution: %v", err)
				debuglog.Log("entering phaseDone: %s", m.completionReason)
				m.showCompletionReport()
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

		// Usage limit — pause everything and wait for user
		if u.UsageLimit {
			m.pausedDuring = phaseParallel
			m.phase = phasePaused
			m.claudeContent += fmt.Sprintf("\n── Usage Limit Hit (%s) ──\nClaude API usage limit reached. All workers paused.\nPress Enter to resume when your limit resets.\n", u.StoryID)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			return m, nil
		}

		// Auto-select first worker if none selected yet
		if m.activeWorkerView == 0 {
			m.activeWorkerView = u.WorkerID
		}

		// Display judge result if present
		if u.JudgeResult != nil {
			m.judgeContent += judge.FormatResult(u.StoryID, *u.JudgeResult)
			m.ctxMode = contextJudge
			judge.AppendJudgeResult(m.cfg.ProgressFile, u.StoryID, *u.JudgeResult)
		}

		switch u.State {
		case worker.WorkerDone:
			// Cache the activity log before workspace cleanup
			m.cacheWorkerLog(u.WorkerID)
			if u.Passed && u.ChangeID != "" {
				cmds = append(cmds, mergeBackCmd(m.ctx, m.coord, u))
			} else {
				// Abandon the committed change so it doesn't leave an orphaned
				// side branch in the jj history.
				if u.ChangeID != "" {
					_ = workspace.AbandonChange(m.ctx, m.cfg.ProjectDir, u.ChangeID)
				}
				// Preserve activity log for debugging before workspace is destroyed
				m.coord.PreserveFailedLogs(u.StoryID, u.WorkerID)
				go m.coord.CleanupWorker(m.ctx, u.WorkerID)
				if willRetry {
					m.claudeContent += fmt.Sprintf("\n── Worker %d (%s): story did not pass — retrying ──\n", u.WorkerID, u.StoryID)
					m.claudeVP.SetContent(m.claudeContent)
					m.claudeVP.GotoBottom()
					m.prevClaudeLen = len(m.claudeContent)
				}
				// Try to schedule more
				m.coord.ScheduleReady(m.ctx)
				if m.coord.AllDone() {
					if m.coord.CompletedCount() == m.totalStories {
						return m.transitionToComplete()
					}
					m.phase = phaseDone
					m.allComplete = false
					m.exitCode = 1
					m.completionReason = fmt.Sprintf("Parallel workers done but only %d/%d stories completed (worker did not pass)", m.coord.CompletedCount(), m.totalStories)
					debuglog.Log("entering phaseDone: %s", m.completionReason)
					m.showCompletionReport()
					return m, nil
				}
			}
		case worker.WorkerFailed:
			m.cacheWorkerLog(u.WorkerID)
			// Preserve activity log for debugging before workspace is destroyed
			m.coord.PreserveFailedLogs(u.StoryID, u.WorkerID)
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
				m.completionReason = fmt.Sprintf("Worker failed and all work done (%d/%d completed)", m.coord.CompletedCount(), m.totalStories)
				debuglog.Log("entering phaseDone: %s", m.completionReason)
				m.showCompletionReport()
				return m, nil
			}
		}
		// Only keep listening if there are active workers; otherwise we'd
		// block forever on the update channel with no one sending.
		if m.coord.ActiveCount() > 0 {
			cmds = append(cmds, m.coord.ListenCmd())
		} else if m.coord.AllDone() {
			// No active workers and nothing left to schedule — we're done
			m.phase = phaseDone
			m.allComplete = m.coord.CompletedCount() == m.totalStories
			if !m.allComplete {
				m.exitCode = 1
				m.completionReason = fmt.Sprintf("No active workers remaining (%d/%d completed)", m.coord.CompletedCount(), m.totalStories)
				debuglog.Log("entering phaseDone: %s", m.completionReason)
				m.showCompletionReport()
			}
			return m, nil
		}

	case coordinator.MergeCompleteMsg:
		if msg.Err != nil {
			// Abandon the change so it doesn't leave an orphaned side branch.
			if msg.ChangeID != "" {
				_ = workspace.AbandonChange(m.ctx, m.cfg.ProjectDir, msg.ChangeID)
			}
			m.claudeContent += fmt.Sprintf("\n── Merge failed (%s): %v ──\n", msg.StoryID, msg.Err)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
		} else if msg.ConflictsResolved {
			m.claudeContent += fmt.Sprintf("\n── Merged %s into main (conflicts resolved) ──\n", msg.StoryID)
		} else {
			m.claudeContent += fmt.Sprintf("\n── Merged %s into main ──\n", msg.StoryID)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			// Update story counts immediately (don't wait for slow tick)
			m.completedStories = m.coord.CompletedCount()
		}
		m.cacheWorkerLog(msg.WorkerID)
		go m.coord.CleanupWorker(m.ctx, msg.WorkerID)
		// Trigger embedding pipeline after successful merge
		if msg.Err == nil && m.chromaClient != nil && m.memoryEmbedder != nil {
			ss, _ := storystate.Load(m.cfg.ProjectDir, msg.StoryID)
			contextExhausted := ss.Status == storystate.StatusContextExhausted
			cmds = append(cmds, runPipelineCmd(m.ctx, m.chromaClient, m.memoryEmbedder, m.cfg.ProjectDir, msg.StoryID, contextExhausted))
		}
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
			m.completionReason = fmt.Sprintf("All parallel work done after merge but only %d/%d stories completed", m.completedStories, m.totalStories)
			debuglog.Log("entering phaseDone: %s", m.completionReason)
			m.showCompletionReport()
			return m, nil
		}

	case coordinator.WorkerActivityMsg:
		// Always update the cache with latest content
		m.workerLogCache[msg.WorkerID] = msg.Content
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
		debuglog.Log("judgeDone: story=%s passed=%v reason=%s", m.currentStoryID, msg.Result.Passed, msg.Result.Reason)
		// Show judge result in the context panel
		m.judgeContent += judge.FormatResult(m.currentStoryID, msg.Result)
		m.ctxMode = contextJudge

		// Persist judge result to progress.md
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
		m.ctxMode = contextQuality
		if msg.Err != nil {
			errMsg := fmt.Sprintf("\n── Quality review error: %v ──\n", msg.Err)
			m.qualityContent += errMsg
			m.claudeContent += errMsg
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			return m.transitionToSummary()
		}

		m.lastAssessment = &msg.Assessment
		summary := quality.FormatSummary(msg.Assessment)
		m.claudeContent += "\n" + summary
		m.qualityContent += "\n" + summary
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)

		if msg.Assessment.TotalFindings() == 0 {
			var statusMsg string
			if msg.Assessment.HasParseFailures() {
				statusMsg = "\n── Quality review: no findings parsed (some lenses failed to parse) ──\n"
			} else {
				statusMsg = "\n── Quality review: all clean! ──\n"
			}
			m.claudeContent += statusMsg
			m.qualityContent += statusMsg
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			return m.transitionToSummary()
		}

		// Has findings — start fix phase
		m.phase = phaseQualityFix
		fixMsg := "\n── Fixing quality issues... ──\n"
		m.claudeContent += fixMsg
		m.qualityContent += fixMsg
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)
		cmds = append(cmds, qualityFixCmd(m.ctx, m.cfg, msg.Assessment, m.qualityIteration))

	case qualityFixDoneMsg:
		if msg.Err != nil {
			errMsg := fmt.Sprintf("\n── Quality fix error: %v ──\n", msg.Err)
			m.claudeContent += errMsg
			m.qualityContent += errMsg
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
		}

		if m.qualityIteration >= m.cfg.QualityMaxIters {
			// Max iterations reached — prompt user
			m.phase = phaseQualityPrompt
			maxMsg := "\n── Max quality iterations reached. Press Enter to continue fixing, q to finish ──\n"
			m.claudeContent += maxMsg
			m.qualityContent += maxMsg
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			return m, nil
		}

		// Re-review
		m.qualityIteration++
		m.phase = phaseQualityReview
		reReviewMsg := fmt.Sprintf("\n── Re-reviewing (iteration %d)... ──\n", m.qualityIteration)
		m.claudeContent += reReviewMsg
		m.qualityContent += reReviewMsg
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)
		cmds = append(cmds, qualityReviewCmd(m.ctx, m.cfg, m.qualityIteration))

	case summaryDoneMsg:
		if msg.Err != nil {
			m.claudeContent += fmt.Sprintf("\n── Summary generation error: %v ──\n", msg.Err)
		}
		if msg.Content != "" {
			m.claudeContent = msg.Content
		} else {
			m.claudeContent += "\n── Summary generation complete (no SUMMARY.md produced) ──\n"
		}
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)
		// Best-effort checkpoint cleanup on clean completion
		_ = checkpoint.Delete(m.cfg.ProjectDir)
		m.phase = phaseDone
		m.allComplete = true
		m.exitCode = 0
		m.completionReason = "All stories completed successfully"
		debuglog.Log("entering phaseDone: %s", m.completionReason)
	}

	return m, tea.Batch(cmds...)
}

// transitionToComplete handles the "all stories done" transition.
// If quality review is enabled and hasn't run yet, starts quality review.
// Otherwise, transitions to summary generation.
func (m *Model) transitionToComplete() (tea.Model, tea.Cmd) {
	debuglog.Log("transitionToComplete: iteration=%d, currentStory=%s", m.iteration, m.currentStoryID)
	if m.cfg.QualityReview && m.qualityIteration == 0 {
		m.qualityIteration = 1
		m.phase = phaseQualityReview
		m.claudeContent = "── Starting quality review ──\n"
		m.claudeVP.SetContent(m.claudeContent)
		m.prevClaudeLen = len(m.claudeContent)
		return m, qualityReviewCmd(m.ctx, m.cfg, m.qualityIteration)
	}
	return m.transitionToSummary()
}

// transitionToSummary starts generating a final summary of all changes.
func (m *Model) transitionToSummary() (tea.Model, tea.Cmd) {
	// Run confidence decay cycle before stopping sidecar (needs ChromaDB running)
	if m.chromaClient != nil && m.confirmTracker != nil {
		summary, err := memory.RunDecayCycle(m.ctx, m.chromaClient, m.confirmTracker)
		if err != nil {
			debuglog.Log("warning: memory decay cycle failed: %v", err)
		} else {
			debuglog.Log("Memory maintenance: %d confirmed, %d decayed, %d evicted",
				summary.Confirmed, summary.Decayed, summary.Evicted)
		}
	}
	// Stop ChromaDB sidecar — no more memory operations needed
	m.stopSidecar()
	// Best-effort checkpoint cleanup on clean completion
	_ = checkpoint.Delete(m.cfg.ProjectDir)
	m.phase = phaseSummary
	m.claudeContent = "── Generating summary of all changes... ──\n"
	m.claudeVP.SetContent(m.claudeContent)
	m.prevClaudeLen = len(m.claudeContent)
	return m, generateSummaryCmd(m.ctx, m.cfg)
}

// cacheWorkerLog reads the activity log for a worker and stores it in memory.
func (m *Model) cacheWorkerLog(wID worker.WorkerID) {
	if m.coord == nil {
		return
	}
	// If we already have content from the activity poll, it's already cached
	if _, ok := m.workerLogCache[wID]; ok {
		return
	}
	// Try to read from disk as fallback
	actPath := m.coord.GetWorkerActivityPath(wID)
	if actPath == "" {
		return
	}
	if data, err := os.ReadFile(actPath); err == nil {
		m.workerLogCache[wID] = string(data)
	}
}

// writeSerialCheckpoint writes a checkpoint after each serial iteration completes.
func (m *Model) writeSerialCheckpoint(iterErr error) {
	p, err := prd.Load(m.cfg.PRDFile)
	if err != nil {
		return
	}

	var completed []string
	failed := make(map[string]checkpoint.FailedStory)

	for _, s := range p.UserStories {
		if s.Passes {
			completed = append(completed, s.ID)
		}
	}

	// If the current iteration had an error, record the current story as failed
	if iterErr != nil && m.currentStoryID != "" {
		failed[m.currentStoryID] = checkpoint.FailedStory{
			Retries:   1,
			LastError: iterErr.Error(),
		}
	}

	cp := checkpoint.Checkpoint{
		PRDHash:          m.prdHash,
		Phase:            "serial",
		CompletedStories: completed,
		FailedStories:    failed,
		InProgress:       nil,
		DAG:              nil,
		IterationCount:   m.iteration,
		Timestamp:        time.Now(),
	}

	_ = checkpoint.Save(m.cfg.ProjectDir, cp)
}

func (m *Model) handleJudgeCheck() tea.Cmd {
	// Skip judge if no pre-revisions were captured
	if len(m.preRevs) == 0 {
		debuglog.Log("handleJudgeCheck: skipping judge for %s — no pre-revisions", m.currentStoryID)
		return nil
	}

	// Reload PRD to check if story passes
	p, err := prd.Load(m.cfg.PRDFile)
	if err != nil {
		debuglog.Log("handleJudgeCheck: prd load error: %v", err)
		return nil
	}
	story := p.FindStory(m.currentStoryID)
	if story == nil || !story.Passes {
		debuglog.Log("handleJudgeCheck: story %s not passing (story=%v), skipping judge", m.currentStoryID, story != nil)
		return nil
	}

	// Story claims to pass — run judge
	rejections := judge.GetRejectionCount(m.cfg.ProjectDir, m.currentStoryID)
	if rejections >= m.cfg.JudgeMaxRejections {
		debuglog.Log("handleJudgeCheck: auto-passing %s after %d rejections", m.currentStoryID, rejections)
		// Auto-pass
		judge.AppendAutoPass(m.cfg.ProgressFile, m.currentStoryID, rejections)
		judge.ClearRejectionCount(m.cfg.ProjectDir, m.currentStoryID)
		m.judgeContent += fmt.Sprintf("\n── Judge: %s ── AUTO-PASS after %d rejections [HUMAN REVIEW NEEDED] ──\n", m.currentStoryID, rejections)
		m.ctxMode = contextJudge
		return nil
	}

	m.phase = phaseJudgeRun
	m.judgeContent += "\n── Judge reviewing " + m.currentStoryID + "... ──\n"
	m.ctxMode = contextJudge
	return runJudgeCmd(m.ctx, m.cfg, m.currentStoryID, m.preRevs)
}

func (m *Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	if m.phase == phaseResumePrompt && m.loadedCheckpoint != nil {
		return m.renderResumePrompt()
	}

	// Layout: header(3) + top panels + claude activity + footer(1)
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

	storiesWidth := m.width * 35 / 100
	contextWidth := m.width - storiesWidth

	// Render header
	header := renderHeader(m, m.width)

	// Stories panel
	storiesPanel := renderStoriesPanel(
		&m.storiesVP,
		m.storyDisplayInfos,
		m.activePanel == panelStories,
		storiesWidth,
		topHeight,
		m.animFrame,
	)

	// Context panel
	ctxData := contextPanelData{
		Mode:            m.ctxMode,
		ProgressContent: m.progressContent,
		ProgressChanged: m.progressChanged,
		WorktreeContent: m.worktreeContent,
		JudgeContent:    m.judgeContent,
		QualityContent:  m.qualityContent,
		MemoryContent:   m.memoryContent,
		Phase:           m.phase,
	}
	ctxPanel := renderContextPanel(
		&m.contextVP,
		ctxData,
		m.activePanel == panelContext,
		contextWidth,
		topHeight,
	)
	m.progressChanged = false

	topRow := lipgloss.JoinHorizontal(lipgloss.Top, storiesPanel, ctxPanel)

	// Claude panel
	claudeRunning := m.phase == phaseClaudeRun || m.phase == phaseJudgeRun || m.phase == phaseParallel || m.phase == phaseDagAnalysis || m.phase == phasePlanning || m.phase == phaseQualityReview || m.phase == phaseQualityFix || m.phase == phaseSummary
	var workerTabStr string
	if m.phase == phaseParallel && m.coord != nil {
		workers := m.coord.Workers()
		// Sort: active workers first (by ID), then completed/failed (by ID)
		var activeIDs, doneIDs []worker.WorkerID
		for id, w := range workers {
			if w.State == worker.WorkerIdle {
				continue
			}
			if w.State == worker.WorkerDone || w.State == worker.WorkerFailed {
				doneIDs = append(doneIDs, id)
			} else {
				activeIDs = append(activeIDs, id)
			}
		}
		sort.Slice(activeIDs, func(i, j int) bool { return activeIDs[i] < activeIDs[j] })
		sort.Slice(doneIDs, func(i, j int) bool { return doneIDs[i] < doneIDs[j] })
		m.workerTabOrder = append(activeIDs, doneIDs...)

		var tabParts []string
		for tabIdx, id := range m.workerTabOrder {
			w := workers[id]
			marker := ""
			if id == m.activeWorkerView {
				marker = "▸"
			}
			tabParts = append(tabParts, fmt.Sprintf("%s%d:%s[%s]", marker, tabIdx+1, w.StoryID, w.State))
		}
		workerTabStr = strings.Join(tabParts, " │ ")
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

	footer := renderFooter(m.width, m.confirmQuit, m.phase == phaseDone, m.phase == phaseIdle, m.phase == phaseParallel, m.phase == phaseReview, m.phase == phaseQualityPrompt, m.phase == phaseResumePrompt, m.phase == phasePaused)

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

// renderResumePrompt renders the checkpoint resume prompt as a full-screen view.
func (m *Model) renderResumePrompt() string {
	cp := m.loadedCheckpoint

	var b strings.Builder
	header := renderHeader(m, m.width)
	b.WriteString(header)
	b.WriteString("\n")

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFD700")).Render("  Checkpoint Found — Resume Previous Run?")
	b.WriteString(title)
	b.WriteString("\n\n")

	// Mode
	b.WriteString(fmt.Sprintf("  Mode: %s\n\n", stylePanelTitle.Render(cp.Phase)))

	// Completed stories
	b.WriteString(fmt.Sprintf("  %s (%d)\n", styleSuccess.Render("Completed"), len(cp.CompletedStories)))
	for _, id := range cp.CompletedStories {
		b.WriteString(fmt.Sprintf("    %s %s\n", styleStoryPassed.Render("✓"), id))
	}
	if len(cp.CompletedStories) == 0 {
		b.WriteString("    (none)\n")
	}
	b.WriteString("\n")

	// Failed stories
	b.WriteString(fmt.Sprintf("  %s (%d)\n", styleDanger.Render("Failed"), len(cp.FailedStories)))
	for id, fs := range cp.FailedStories {
		errSummary := fs.LastError
		if len(errSummary) > 60 {
			errSummary = errSummary[:60] + "..."
		}
		b.WriteString(fmt.Sprintf("    %s %s (retries: %d) %s\n", styleStoryFailed.Render("✗"), id, fs.Retries, styleMuted.Render(errSummary)))
	}
	if len(cp.FailedStories) == 0 {
		b.WriteString("    (none)\n")
	}
	b.WriteString("\n")

	// Remaining count
	totalKnown := len(cp.CompletedStories) + len(cp.FailedStories) + len(cp.InProgress)
	if p, err := prd.Load(m.cfg.PRDFile); err == nil {
		remaining := p.TotalCount() - len(cp.CompletedStories)
		b.WriteString(fmt.Sprintf("  %s %d\n\n", styleMuted.Render("Remaining:"), remaining))
	} else {
		b.WriteString(fmt.Sprintf("  %s %d+ stories tracked\n\n", styleMuted.Render("Total:"), totalKnown))
	}

	// Prompt
	b.WriteString("  Press " + styleKey.Render("y") + " to resume, " + styleKey.Render("n") + " to start fresh, " + styleKey.Render("q") + " to quit\n")

	return b.String()
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

func renderFooter(width int, confirmQuit bool, done bool, idle bool, parallel bool, review bool, qualityPrompt bool, resumePrompt bool, paused bool) string {
	if paused {
		return "  " + styleKey.Render("enter") + styleFooter.Render(": resume  ") +
			styleKey.Render("q") + styleFooter.Render(": quit")
	}
	if resumePrompt {
		return "  " + styleKey.Render("y") + styleFooter.Render(": resume  ") +
			styleKey.Render("n") + styleFooter.Render(": start fresh  ") +
			styleKey.Render("q") + styleFooter.Render(": quit")
	}
	if confirmQuit {
		return "  " + styleQuitConfirm.Render("Press q again to quit, any other key to cancel")
	}
	baseHelp := styleKey.Render("q") + styleFooter.Render(": quit  ") +
		styleKey.Render("tab") + styleFooter.Render(": panel  ") +
		styleKey.Render("[/]") + styleFooter.Render(": context tab  ") +
		styleKey.Render("j/k") + styleFooter.Render(": scroll")
	if parallel {
		baseHelp += "  " + styleKey.Render("1-9") + styleFooter.Render(": worker")
	}
	if qualityPrompt {
		return "  " + styleKey.Render("enter") + styleFooter.Render(": continue fixing  ") +
			styleKey.Render("q") + styleFooter.Render(": finish  ") +
			styleKey.Render("[/]") + styleFooter.Render(": context tab  ") +
			styleKey.Render("j/k") + styleFooter.Render(": scroll")
	}
	if review {
		return "  " + styleKey.Render("enter") + styleFooter.Render(": execute  ") +
			styleKey.Render("q") + styleFooter.Render(": quit  ") +
			styleKey.Render("[/]") + styleFooter.Render(": context tab  ") +
			styleKey.Render("j/k") + styleFooter.Render(": scroll")
	}
	if idle {
		return "  " + styleMuted.Render("Idle — ") + baseHelp
	}
	if done {
		return "  " + styleSuccess.Render("Run complete — ") + baseHelp
	}
	return "  " + baseHelp
}

// generateCompletionReport builds a human-readable report of the run outcome,
// explaining which stories were completed, which were not, and why.
func (m *Model) generateCompletionReport() string {
	var sb strings.Builder

	sb.WriteString("\n── Completion Report ──\n\n")
	sb.WriteString(fmt.Sprintf("Reason: %s\n", m.completionReason))
	sb.WriteString(fmt.Sprintf("Duration: %s\n", time.Since(m.startTime).Truncate(time.Second)))
	sb.WriteString(fmt.Sprintf("Iterations used: %d\n\n", m.iteration))

	p, err := prd.Load(m.cfg.PRDFile)
	if err != nil {
		sb.WriteString(fmt.Sprintf("Could not load prd.json: %v\n", err))
		return sb.String()
	}

	var passed, incomplete []prd.UserStory
	for _, s := range p.UserStories {
		if s.Passes {
			passed = append(passed, s)
		} else {
			incomplete = append(incomplete, s)
		}
	}

	sb.WriteString(fmt.Sprintf("Stories: %d/%d completed\n\n", len(passed), len(p.UserStories)))

	if len(passed) > 0 {
		sb.WriteString("Completed:\n")
		for _, s := range passed {
			sb.WriteString(fmt.Sprintf("  ✓ %s: %s\n", s.ID, s.Title))
		}
		sb.WriteString("\n")
	}

	if len(incomplete) > 0 {
		sb.WriteString("Incomplete:\n")
		for _, s := range incomplete {
			reason := m.inferStorySkipReason(s.ID)
			sb.WriteString(fmt.Sprintf("  ✗ %s: %s\n    Reason: %s\n", s.ID, s.Title, reason))
		}
	}

	// Write report to log file
	reportPath := filepath.Join(m.cfg.LogDir, "completion-report.log")
	_ = os.WriteFile(reportPath, []byte(sb.String()), 0o644)

	return sb.String()
}

// inferStorySkipReason tries to determine why a story was not completed.
func (m *Model) inferStorySkipReason(storyID string) string {
	// Check parallel coordinator state
	if m.coord != nil {
		if m.coord.IsCompleted(storyID) {
			return "Marked completed by coordinator but passes=false in prd.json (possible judge rejection)"
		}
		if m.coord.IsFailed(storyID) {
			if errMsg := m.coord.FailedError(storyID); errMsg != "" {
				return fmt.Sprintf("Worker failed: %s", errMsg)
			}
			return "Worker failed (no error details)"
		}
		// Check if it was blocked by a failed dependency
		if blocked, dep := m.coord.IsBlockedByFailure(storyID); blocked {
			return fmt.Sprintf("Blocked: dependency %q failed", dep)
		}
		return "Not scheduled (may have been unreachable in DAG)"
	}

	// Serial mode: check if we hit max iterations
	if m.iteration > m.cfg.MaxIterations {
		return fmt.Sprintf("Max iterations reached (%d)", m.cfg.MaxIterations)
	}

	// Check events log for clues about this story
	evts, err := events.Load(m.cfg.ProjectDir)
	if err == nil {
		for i := len(evts) - 1; i >= 0; i-- {
			e := evts[i]
			if e.StoryID != storyID {
				continue
			}
			switch e.Type {
			case events.EventStuck:
				return fmt.Sprintf("Got stuck: %s", e.Summary)
			case events.EventContextExhausted:
				return "Claude ran out of context window"
			case events.EventStoryFailed:
				return fmt.Sprintf("Failed: %s", e.Summary)
			}
		}
	}

	return "Not attempted (run ended before this story was reached)"
}

// showCompletionReport generates and displays the completion report in the Claude panel.
func (m *Model) showCompletionReport() {
	report := m.generateCompletionReport()
	debuglog.Log("completion report:\n%s", report)
	m.claudeContent += report
	m.claudeVP.SetContent(m.claudeContent)
	m.claudeVP.GotoBottom()
	m.prevClaudeLen = len(m.claudeContent)
}

