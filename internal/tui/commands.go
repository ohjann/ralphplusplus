package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/eoghanhynes/ralph/internal/archive"
	"github.com/eoghanhynes/ralph/internal/autofix"
	"github.com/eoghanhynes/ralph/internal/debuglog"
	"github.com/eoghanhynes/ralph/internal/config"
	"github.com/eoghanhynes/ralph/internal/coordinator"
	"github.com/eoghanhynes/ralph/internal/dag"
	rexec "github.com/eoghanhynes/ralph/internal/exec"
	"github.com/eoghanhynes/ralph/internal/judge"
	"github.com/eoghanhynes/ralph/internal/memory"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/quality"
	"github.com/eoghanhynes/ralph/internal/runner"
	"github.com/eoghanhynes/ralph/internal/worker"
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

func planCmd(ctx context.Context, cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		planContent, err := os.ReadFile(cfg.PlanFile)
		if err != nil {
			return planDoneMsg{Err: fmt.Errorf("reading plan file: %w", err)}
		}

		prompt := fmt.Sprintf(`You are generating a prd.json file from a plan. Read the plan below, explore the codebase for context, then generate prd.json.

CRITICAL: Write prd.json to the current working directory using the Write tool. Do NOT write it anywhere else. The file MUST be named exactly "prd.json".

## prd.json Format

The file must be valid JSON with this exact structure:

{
  "project": "<short project name>",
  "branchName": "<kebab-case branch name for this work>",
  "description": "<one-line description of the work>",
  "userStories": [
    {
      "id": "<PREFIX-001>",
      "title": "<short title>",
      "description": "<As a [user], I want [feature] so that [benefit]>",
      "acceptanceCriteria": [
        "Specific verifiable criterion",
        "Another criterion",
        "Typecheck passes"
      ],
      "priority": 1,
      "passes": false,
      "notes": ""
    }
  ]
}

Story IDs should use a short prefix derived from the project name (e.g., "TP-001" for "Task Priority").
Priority numbers determine execution order: 1 runs first, 2 runs second, etc.
All stories must have "passes": false and "notes": "".

## Story Sizing Rules
- Each story must be completable in ONE Claude Code context window (one focused session)
- Right-sized examples: add a DB column, add a UI component, update server logic, add an API endpoint
- Too big (MUST split): "build entire dashboard", "add authentication", "create full CRUD" — split these into smaller stories
- When in doubt, make stories smaller rather than larger

## Story Ordering
- Schema/database changes first, then backend/API, then UI/frontend
- Earlier stories must NOT depend on later ones
- Each story should be independently testable after completion

## Acceptance Criteria
- Must be specific and verifiable, not vague
- BAD: "Works correctly", "Is fast", "Looks good"
- GOOD: "Returns 200 with JSON body containing user object", "Button shows confirmation dialog before deleting"
- Always include "Typecheck passes" for every story
- UI stories: always include "Verify in browser"

## The Plan

%s
`, string(planContent))

		// Ensure log directory exists
		_ = os.MkdirAll(cfg.LogDir, 0o755)

		logPath := filepath.Join(cfg.LogDir, "plan.log")
		err = runner.RunClaude(ctx, cfg.ProjectDir, prompt, logPath)
		if err != nil {
			return planDoneMsg{Err: fmt.Errorf("claude plan generation failed: %w", err)}
		}

		// Verify prd.json was actually created
		if _, statErr := os.Stat(cfg.PRDFile); os.IsNotExist(statErr) {
			return planDoneMsg{Err: fmt.Errorf("claude did not generate prd.json")}
		}

		return planDoneMsg{}
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
			debuglog.Log("findNextStory: prd load error: %v — signaling AllDone", err)
			return nextStoryMsg{AllDone: true}
		}
		if p.AllComplete() {
			debuglog.Log("findNextStory: all %d stories complete", p.TotalCount())
			return nextStoryMsg{AllDone: true}
		}
		next := p.NextIncompleteStory()
		if next == nil {
			debuglog.Log("findNextStory: AllComplete()=false but NextIncompleteStory()=nil (should not happen)")
			return nextStoryMsg{AllDone: true}
		}
		debuglog.Log("findNextStory: next story=%s (%d/%d complete)", next.ID, p.CompletedCount(), p.TotalCount())
		return nextStoryMsg{StoryID: next.ID, StoryTitle: next.Title}
	}
}

func runClaudeCmd(ctx context.Context, cfg *config.Config, storyID string, iteration int, chromaClient *memory.ChromaClient, embedder memory.Embedder) tea.Cmd {
	return func() tea.Msg {
		p, err := prd.Load(cfg.PRDFile)
		if err != nil {
			return claudeDoneMsg{Err: fmt.Errorf("loading PRD: %w", err)}
		}

		var opts []runner.BuildPromptOpts
		if chromaClient != nil && embedder != nil && !cfg.Memory.Disabled {
			retriever := memory.NewRetriever(chromaClient, embedder)
			if retriever != nil {
				opts = append(opts, runner.BuildPromptOpts{
					Memory: retriever,
					MemoryOpts: memory.RetrievalOptions{
						TopK:      cfg.Memory.TopK,
						MinScore:  cfg.Memory.MinScore,
						MaxTokens: cfg.Memory.MaxTokens,
					},
				})
			}
		}

		prompt, err := runner.BuildPrompt(cfg.RalphHome, cfg.ProjectDir, storyID, p, opts...)
		if err != nil {
			return claudeDoneMsg{Err: err}
		}

		logPath := runner.LogFilePath(cfg.LogDir, iteration)
		err = runner.RunClaude(ctx, cfg.ProjectDir, prompt, logPath, runner.RunClaudeOpts{
			Iteration: iteration,
			StoryID:   storyID,
		})

		completeSignal := runner.LogContainsComplete(logPath)

		return claudeDoneMsg{Err: err, CompleteSignal: completeSignal}
	}
}

func generateFixStoryCmd(ctx context.Context, cfg *config.Config, info runner.StuckInfo) tea.Cmd {
	return func() tea.Msg {
		p, err := prd.Load(cfg.PRDFile)
		if err != nil {
			return fixStoryGeneratedMsg{Err: err}
		}
		original := p.FindStory(info.StoryID)
		if original == nil {
			return fixStoryGeneratedMsg{Err: fmt.Errorf("story %s not found", info.StoryID)}
		}

		activityPath := runner.ActivityFilePath(cfg.LogDir, info.Iteration)
		activityTail := runner.ReadLogTail(activityPath, 50)

		fix, err := autofix.GenerateFixStory(ctx, info, *original, activityTail)
		if err != nil {
			return fixStoryGeneratedMsg{Err: err}
		}

		if err := autofix.InsertFixStory(cfg.PRDFile, fix, info.StoryID); err != nil {
			return fixStoryGeneratedMsg{Err: err}
		}

		return fixStoryGeneratedMsg{StoryID: fix.ID}
	}
}

func pollStuckCmd(projectDir string, iteration int) tea.Cmd {
	return func() tea.Msg {
		info := runner.ReadStuckInfo(projectDir, iteration)
		if info != nil {
			return stuckDetectedMsg{Info: *info}
		}
		return nil
	}
}

func runJudgeCmd(ctx context.Context, cfg *config.Config, storyID string, preRevs []judge.DirRev) tea.Cmd {
	return func() tea.Msg {
		result := judge.RunJudge(ctx, cfg.RalphHome, cfg.ProjectDir, cfg.PRDFile, storyID, preRevs)
		return judgeDoneMsg{Result: result}
	}
}

func captureRevsCmd(ctx context.Context, dirs []string) []judge.DirRev {
	var revs []judge.DirRev
	for _, dir := range dirs {
		rev, _ := rexec.JJCurrentRev(ctx, dir)
		revs = append(revs, judge.DirRev{Dir: dir, Rev: rev})
	}
	return revs
}

func dagAnalyzeCmd(ctx context.Context, cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		p, err := prd.Load(cfg.PRDFile)
		if err != nil {
			return coordinator.DAGAnalyzedMsg{Err: err}
		}

		// Only analyze incomplete stories
		var incomplete []prd.UserStory
		for _, s := range p.UserStories {
			if !s.Passes {
				incomplete = append(incomplete, s)
			}
		}

		if len(incomplete) == 0 {
			return coordinator.DAGAnalyzedMsg{Err: fmt.Errorf("no incomplete stories")}
		}

		d, err := dag.Analyze(ctx, cfg.ProjectDir, incomplete)
		if err != nil {
			// Fallback to linear
			d = dag.LinearFallback(incomplete)
			return coordinator.DAGAnalyzedMsg{DAG: d}
		}

		// Validate
		ids := make([]string, len(incomplete))
		for i, s := range incomplete {
			ids[i] = s.ID
		}
		if err := d.Validate(ids); err != nil {
			d = dag.LinearFallback(incomplete)
		}

		return coordinator.DAGAnalyzedMsg{DAG: d}
	}
}

func mergeBackCmd(ctx context.Context, coord *coordinator.Coordinator, u worker.WorkerUpdate) tea.Cmd {
	return func() tea.Msg {
		conflictsResolved, err := coord.MergeAndSync(ctx, u)
		return coordinator.MergeCompleteMsg{
			StoryID:           u.StoryID,
			WorkerID:          u.WorkerID,
			ChangeID:          u.ChangeID,
			Err:               err,
			ConflictsResolved: conflictsResolved,
		}
	}
}

func pollWorkerActivityCmd(wID worker.WorkerID, activityPath string) tea.Cmd {
	return func() tea.Msg {
		content := runner.ReadActivityContent(activityPath)
		return coordinator.WorkerActivityMsg{
			WorkerID: wID,
			Content:  content,
		}
	}
}

func qualityReviewCmd(ctx context.Context, cfg *config.Config, iteration int) tea.Cmd {
	return func() tea.Msg {
		manifest, err := quality.GetDiffManifest(ctx, cfg.ProjectDir)
		if err != nil || manifest == "" {
			return qualityReviewDoneMsg{Err: fmt.Errorf("no changes to review: %v", err)}
		}

		lenses := quality.DefaultLenses()
		results := quality.RunReviewsParallel(ctx, cfg.ProjectDir, cfg.LogDir, lenses, manifest, iteration, cfg.QualityWorkers)
		assessment := quality.MergeAssessment(results, iteration)

		_ = quality.WriteAssessment(cfg.ProjectDir, assessment)

		return qualityReviewDoneMsg{Assessment: assessment}
	}
}

func qualityFixCmd(ctx context.Context, cfg *config.Config, assessment quality.Assessment, iteration int) tea.Cmd {
	return func() tea.Msg {
		err := quality.RunFix(ctx, cfg.ProjectDir, cfg.LogDir, assessment, iteration)
		return qualityFixDoneMsg{Err: err}
	}
}

func generateSummaryCmd(ctx context.Context, cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		// Read PRD for context
		prdData, _ := os.ReadFile(cfg.PRDFile)
		// Read progress for context
		progressData, _ := os.ReadFile(cfg.ProgressFile)

		prompt := fmt.Sprintf(`You have just completed implementing all stories in a project. Generate a comprehensive summary of everything that was done.

Write this summary to a file called SUMMARY.md in the current working directory using the Write tool.

The summary should include:
1. **Overview** - What was built/changed (one paragraph)
2. **Stories Completed** - Brief summary of each story and what it involved
3. **Files Changed** - Key files that were added or modified (explore the recent changes)
4. **Configuration** - Any new configuration, environment variables, or setup needed
5. **Build & Run** - How to build and run the project (check for Makefile, package.json, etc.)
6. **Testing** - How to run tests, any new test files added
7. **Notes** - Any caveats, known issues, or things that need human review

Be concise but thorough. Focus on actionable information the developer needs to know.

## PRD (what was planned)
%s

## Progress Log
%s
`, string(prdData), string(progressData))

		logPath := filepath.Join(cfg.LogDir, "summary.log")
		err := runner.RunClaude(ctx, cfg.ProjectDir, prompt, logPath)

		// Read the generated summary
		summaryPath := filepath.Join(cfg.ProjectDir, "SUMMARY.md")
		content, _ := os.ReadFile(summaryPath)

		return summaryDoneMsg{Content: string(content), Err: err}
	}
}

// chromaSetupCmd sets up the Python environment, starts the ChromaDB sidecar,
// creates all collections, and returns the sidecar + client for storage.
func chromaSetupCmd(ctx context.Context, cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		dataDir := filepath.Join(cfg.RalphHome, "memory")
		port := cfg.Memory.Port
		if port == 0 {
			port = 9876
		}

		// Ensure Python environment with chromadb installed
		pythonPath, err := memory.EnsureChromaDB(dataDir, func(msg string) {
			debuglog.Log("chromadb setup: %s", msg)
		})
		if err != nil {
			return chromaSetupDoneMsg{Err: fmt.Errorf("chromadb setup: %w", err)}
		}

		// Start the sidecar
		sc := &memory.Sidecar{}
		if err := sc.Start(ctx, pythonPath, dataDir, port); err != nil {
			return chromaSetupDoneMsg{Err: fmt.Errorf("chromadb start: %w", err)}
		}

		// Create a client and initialise all collections
		client := memory.NewClient(fmt.Sprintf("http://localhost:%d", sc.Port()))
		for _, coll := range memory.AllCollections() {
			if err := client.CreateCollection(ctx, coll.Name); err != nil {
				// Stop sidecar on collection creation failure
				_ = sc.Stop()
				return chromaSetupDoneMsg{Err: fmt.Errorf("create collection %s: %w", coll.Name, err)}
			}
		}

		debuglog.Log("chromadb sidecar healthy on port %d, all collections ready", sc.Port())
		return chromaSetupDoneMsg{Sidecar: sc, Client: client}
	}
}

// codebaseScanCmd runs the codebase scanner in the background.
func codebaseScanCmd(ctx context.Context, cfg *config.Config, client *memory.ChromaClient, embedder memory.Embedder) tea.Cmd {
	return func() tea.Msg {
		if err := memory.ScanCodebase(ctx, cfg.ProjectDir, client, embedder); err != nil {
			debuglog.Log("codebase scan failed: %v", err)
			return codebaseScanDoneMsg{Err: err}
		}
		debuglog.Log("codebase scan complete")
		return codebaseScanDoneMsg{}
	}
}

// runPipelineCmd runs the embedding pipeline for a completed or context-exhausted story.
// It uses the provided embedder, embeds story data, then enforces collection caps.
func runPipelineCmd(ctx context.Context, client *memory.ChromaClient, embedder memory.Embedder, projectDir, storyID string, contextExhausted bool) tea.Cmd {
	return func() tea.Msg {
		pipeline := memory.NewPipeline(client, embedder)

		var err error
		if contextExhausted {
			err = pipeline.ProcessContextExhaustion(ctx, projectDir, storyID)
		} else {
			err = pipeline.ProcessStoryCompletion(ctx, projectDir, storyID)
		}
		if err != nil {
			debuglog.Log("pipeline embed failed for %s: %v", storyID, err)
			return pipelineEmbedDoneMsg{StoryID: storyID, Err: err}
		}

		// Enforce collection caps on all affected collections
		for name, cap := range memory.CollectionCaps {
			if capErr := memory.EnforceCollectionCap(ctx, client, name, cap); capErr != nil {
				debuglog.Log("pipeline cap enforcement failed for %s: %v", name, capErr)
			}
		}

		debuglog.Log("pipeline embed complete for %s", storyID)
		return pipelineEmbedDoneMsg{StoryID: storyID}
	}
}

// memoryStatsCmd fetches collection statistics and formats them for the memory panel.
func memoryStatsCmd(ctx context.Context, client *memory.ChromaClient, disabled bool) tea.Cmd {
	return func() tea.Msg {
		if disabled {
			return memoryStatsMsg{Content: "  Memory disabled"}
		}
		if client == nil {
			return memoryStatsMsg{Content: "  Memory unavailable (ChromaDB not running)"}
		}

		var sb fmt.Stringer = &memoryStatsBuilder{}
		b := sb.(*memoryStatsBuilder)

		b.WriteString("  Collection Statistics\n")
		b.WriteString("  ─────────────────────\n")

		collections := memory.AllCollections()
		totalDocs := 0
		for _, col := range collections {
			count, err := client.CountDocuments(ctx, col.Name)
			if err != nil {
				b.WriteString(fmt.Sprintf("  %-20s  error\n", col.Name))
				continue
			}
			totalDocs += count
			bar := renderCapBar(count, col.MaxDocuments, 15)
			b.WriteString(fmt.Sprintf("  %-20s %s %4d / %d\n", col.Name, bar, count, col.MaxDocuments))
		}
		b.WriteString(fmt.Sprintf("\n  Total documents: %d\n", totalDocs))

		return memoryStatsMsg{Content: b.String()}
	}
}

// memoryStatsBuilder is a simple string builder implementing fmt.Stringer.
type memoryStatsBuilder struct {
	buf []byte
}

func (b *memoryStatsBuilder) WriteString(s string) {
	b.buf = append(b.buf, s...)
}

func (b *memoryStatsBuilder) String() string {
	return string(b.buf)
}

// renderCapBar renders a simple capacity bar like [████░░░░░░░░░░░].
func renderCapBar(count, max, width int) string {
	if max <= 0 {
		return ""
	}
	filled := count * width / max
	if filled > width {
		filled = width
	}
	bar := make([]byte, width)
	for i := range bar {
		if i < filled {
			bar[i] = '#'
		} else {
			bar[i] = '.'
		}
	}
	return "[" + string(bar) + "]"
}
