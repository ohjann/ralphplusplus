package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/eoghanhynes/ralph/internal/config"
	"github.com/eoghanhynes/ralph/internal/costs"
	"github.com/eoghanhynes/ralph/internal/debuglog"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/tui"
)

var Version = "dev"

func main() {
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Handle history subcommand before validation (no prd.json needed).
	if cfg.HistoryCommand {
		var err error
		switch {
		case cfg.HistoryStats:
			err = printStats(cfg.ProjectDir)
		case cfg.HistoryCompare:
			err = printCompare(cfg.ProjectDir)
		default:
			err = printHistory(cfg.ProjectDir, cfg.HistoryAll)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Handle memory subcommand before validation (no prd.json needed).
	if cfg.MemoryCommand != "" {
		fmt.Fprintf(os.Stderr, "Vector DB memory has been removed. Memory subcommands are no longer available.\n")
		os.Exit(1)
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := cfg.EnsureDirs(); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directories: %v\n", err)
		os.Exit(1)
	}

	// When no prd.json exists, create an empty one for interactive mode.
	if cfg.NoPRD {
		projectName := filepath.Base(cfg.ProjectDir)
		branchName := fmt.Sprintf("ralph/interactive-%s", randomWords())
		emptyPRD := &prd.PRD{
			Project:     projectName,
			BranchName:  branchName,
			UserStories: []prd.UserStory{},
		}
		if err := prd.Save(cfg.PRDFile, emptyPRD); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating empty prd.json: %v\n", err)
			os.Exit(1)
		}
	}

	// Initialize debug log
	if err := debuglog.Init(cfg.LogDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not init debug log: %v\n", err)
	}
	defer debuglog.Close()
	debuglog.Log("ralph starting, version=%s, workers=%d", Version, cfg.Workers)

	model := tui.NewModel(cfg, Version)

	p := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}

	if m, ok := finalModel.(*tui.Model); ok {
		os.Exit(m.ExitCode())
	}
}

func printHistory(projectDir string, showAll bool) error {
	h, err := costs.LoadHistory(projectDir)
	if err != nil {
		return err
	}
	if len(h.Runs) == 0 {
		fmt.Println("No run history yet.")
		return nil
	}

	runs := h.Runs
	if !showAll && len(runs) > 10 {
		runs = runs[len(runs)-10:]
	}

	// Print header
	fmt.Printf("%-19s  %-20s  %9s  %8s  %8s  %9s  %8s  %-10s\n",
		"DATE", "PRD", "STORIES", "COST", "DURATION", "AVG ITER", "1ST PASS", "MODEL")
	fmt.Printf("%-19s  %-20s  %9s  %8s  %8s  %9s  %8s  %-10s\n",
		"───────────────────", "────────────────────", "─────────", "────────", "────────", "─────────", "────────", "──────────")

	for _, r := range runs {
		// Truncate date to first 19 chars (YYYY-MM-DDTHH:MM:SS)
		date := r.Date
		if len(date) > 19 {
			date = date[:19]
		}
		// Truncate PRD name to 20 chars
		prdName := r.PRD
		if len(prdName) > 20 {
			prdName = prdName[:17] + "..."
		}

		stories := fmt.Sprintf("%d/%d", r.StoriesCompleted, r.StoriesTotal)
		cost := fmt.Sprintf("$%.2f", r.TotalCost)
		duration := fmt.Sprintf("%.0f min", r.DurationMinutes)
		avgIter := fmt.Sprintf("%.1f", r.AvgIterationsPerStory)

		firstPass := "-"
		if r.FirstPassRate > 0 || r.StoriesCompleted > 0 {
			firstPass = fmt.Sprintf("%.0f%%", r.FirstPassRate*100)
		}

		model := "-"
		if len(r.ModelsUsed) == 1 {
			model = shortModelName(r.ModelsUsed[0])
		} else if len(r.ModelsUsed) > 1 {
			model = "mixed"
		}

		fmt.Printf("%-19s  %-20s  %9s  %8s  %8s  %9s  %8s  %-10s\n",
			date, prdName, stories, cost, duration, avgIter, firstPass, model)
	}

	if !showAll && len(h.Runs) > 10 {
		fmt.Printf("\nShowing last 10 of %d runs. Use --all to see everything.\n", len(h.Runs))
	}
	return nil
}

// shortModelName extracts a readable name from a full model ID.
// e.g. "claude-opus-4-6" → "opus", "claude-sonnet-4-20250514" → "sonnet"
func shortModelName(model string) string {
	for _, name := range []string{"opus", "sonnet", "haiku"} {
		if strings.Contains(model, name) {
			return name
		}
	}
	if strings.Contains(model, "gemini") {
		return "gemini"
	}
	if len(model) > 10 {
		return model[:10]
	}
	return model
}

func printStats(projectDir string) error {
	h, err := costs.LoadHistory(projectDir)
	if err != nil {
		return err
	}
	if len(h.Runs) == 0 {
		fmt.Println("No run history yet.")
		return nil
	}

	var (
		totalRuns         = len(h.Runs)
		totalStories      int
		totalCompleted    int
		totalFailed       int
		totalIterations   int
		totalInputTokens  int
		totalOutputTokens int
		totalDuration     float64
		firstPassSum      float64
		firstPassCount    int
		cacheHitSum       float64
		cacheHitCount     int
	)

	// Per-story aggregation for most-retried
	type storyAgg struct {
		id       string
		title    string
		rejects  int
		appears  int
	}
	storyMap := make(map[string]*storyAgg)

	for _, r := range h.Runs {
		totalStories += r.StoriesTotal
		totalCompleted += r.StoriesCompleted
		totalFailed += r.StoriesFailed
		totalIterations += r.TotalIterations
		totalInputTokens += r.TotalInputTokens
		totalOutputTokens += r.TotalOutputTokens
		totalDuration += r.DurationMinutes

		if r.FirstPassRate > 0 || r.StoriesCompleted > 0 {
			firstPassSum += r.FirstPassRate
			firstPassCount++
		}
		if r.CacheHitRate > 0 {
			cacheHitSum += r.CacheHitRate
			cacheHitCount++
		}

		for _, sd := range r.StoryDetails {
			agg, ok := storyMap[sd.StoryID]
			if !ok {
				agg = &storyAgg{id: sd.StoryID, title: sd.Title}
				storyMap[sd.StoryID] = agg
			}
			agg.rejects += sd.JudgeRejects
			agg.appears++
		}
	}

	// Print summary
	fmt.Println("── Run Statistics ──")
	fmt.Printf("  Total runs:          %d\n", totalRuns)
	fmt.Printf("  Total stories:       %d completed, %d failed (of %d)\n", totalCompleted, totalFailed, totalStories)
	fmt.Printf("  Total iterations:    %d\n", totalIterations)
	fmt.Printf("  Total duration:      %.0f min\n", totalDuration)
	fmt.Printf("  Total tokens:        %dk in / %dk out\n", totalInputTokens/1000, totalOutputTokens/1000)

	if firstPassCount > 0 {
		fmt.Printf("  Avg first-pass rate: %.0f%%\n", (firstPassSum/float64(firstPassCount))*100)
	}
	if totalCompleted > 0 {
		fmt.Printf("  Avg iterations/story: %.1f\n", float64(totalIterations)/float64(totalCompleted))
	}
	if cacheHitCount > 0 {
		fmt.Printf("  Avg cache hit rate:  %.0f%%\n", (cacheHitSum/float64(cacheHitCount))*100)
	}

	// Last 5 runs trend
	fmt.Println("\n── Recent Trend (last 5 runs) ──")
	start := 0
	if len(h.Runs) > 5 {
		start = len(h.Runs) - 5
	}
	for _, r := range h.Runs[start:] {
		date := r.Date
		if len(date) > 10 {
			date = date[:10]
		}
		fp := "-"
		if r.FirstPassRate > 0 || r.StoriesCompleted > 0 {
			fp = fmt.Sprintf("%.0f%%", r.FirstPassRate*100)
		}
		fmt.Printf("  %s  %d/%d stories  avg %.1f iter  %s 1st-pass\n",
			date, r.StoriesCompleted, r.StoriesTotal, r.AvgIterationsPerStory, fp)
	}

	// Most-retried stories
	type ranked struct {
		id      string
		title   string
		rejects int
	}
	var retried []ranked
	for _, agg := range storyMap {
		if agg.rejects > 0 {
			retried = append(retried, ranked{id: agg.id, title: agg.title, rejects: agg.rejects})
		}
	}
	if len(retried) > 0 {
		// Sort by rejects descending
		for i := 0; i < len(retried); i++ {
			for j := i + 1; j < len(retried); j++ {
				if retried[j].rejects > retried[i].rejects {
					retried[i], retried[j] = retried[j], retried[i]
				}
			}
		}
		fmt.Println("\n── Most Judge-Rejected Stories ──")
		limit := 5
		if len(retried) < limit {
			limit = len(retried)
		}
		for _, r := range retried[:limit] {
			title := r.title
			if len(title) > 40 {
				title = title[:37] + "..."
			}
			fmt.Printf("  %s  %-40s  %d rejects\n", r.id, title, r.rejects)
		}
	}

	return nil
}

type modelGroup struct {
	model         string
	runs          int
	firstPassSum  float64
	firstPassN    int
	avgIterSum    float64
	avgIterN      int
	inputTokens   int
	outputTokens  int
	durationMin   float64
	storiesTotal  int
	storiesDone   int
}

func printCompare(projectDir string) error {
	h, err := costs.LoadHistory(projectDir)
	if err != nil {
		return err
	}
	if len(h.Runs) == 0 {
		fmt.Println("No run history yet.")
		return nil
	}

	// Group runs by primary model
	groups := make(map[string]*modelGroup)

	for _, r := range h.Runs {
		model := "unknown"
		if len(r.ModelsUsed) == 1 {
			model = shortModelName(r.ModelsUsed[0])
		} else if len(r.ModelsUsed) > 1 {
			model = "mixed"
		}

		g, ok := groups[model]
		if !ok {
			g = &modelGroup{model: model}
			groups[model] = g
		}
		g.runs++
		g.inputTokens += r.TotalInputTokens
		g.outputTokens += r.TotalOutputTokens
		g.durationMin += r.DurationMinutes
		g.storiesTotal += r.StoriesTotal
		g.storiesDone += r.StoriesCompleted

		if r.FirstPassRate > 0 || r.StoriesCompleted > 0 {
			g.firstPassSum += r.FirstPassRate
			g.firstPassN++
		}
		if r.StoriesCompleted > 0 {
			g.avgIterSum += r.AvgIterationsPerStory
			g.avgIterN++
		}
	}

	if len(groups) < 2 {
		fmt.Println("── Model Comparison ──")
		fmt.Println("  Only one model group found. Run PRDs with different models to compare.")
		fmt.Println()
		// Still show the single group
		for _, g := range groups {
			printModelGroup(g)
		}
		return nil
	}

	fmt.Println("── Model Comparison ──")
	fmt.Println()

	// Print header
	fmt.Printf("  %-12s  %5s  %9s  %8s  %10s  %10s\n",
		"MODEL", "RUNS", "1ST PASS", "AVG ITER", "IN TOKENS", "OUT TOKENS")
	fmt.Printf("  %-12s  %5s  %9s  %8s  %10s  %10s\n",
		"────────────", "─────", "─────────", "────────", "──────────", "──────────")

	for _, g := range groups {
		fp := "-"
		if g.firstPassN > 0 {
			fp = fmt.Sprintf("%.0f%%", (g.firstPassSum/float64(g.firstPassN))*100)
		}
		ai := "-"
		if g.avgIterN > 0 {
			ai = fmt.Sprintf("%.1f", g.avgIterSum/float64(g.avgIterN))
		}
		fmt.Printf("  %-12s  %5d  %9s  %8s  %9dk  %9dk\n",
			g.model, g.runs, fp, ai, g.inputTokens/1000, g.outputTokens/1000)
	}

	return nil
}

func printModelGroup(g *modelGroup) {
	fp := "-"
	if g.firstPassN > 0 {
		fp = fmt.Sprintf("%.0f%%", (g.firstPassSum/float64(g.firstPassN))*100)
	}
	ai := "-"
	if g.avgIterN > 0 {
		ai = fmt.Sprintf("%.1f", g.avgIterSum/float64(g.avgIterN))
	}
	fmt.Printf("  Model: %s\n", g.model)
	fmt.Printf("    Runs:           %d\n", g.runs)
	fmt.Printf("    Stories:        %d/%d completed\n", g.storiesDone, g.storiesTotal)
	fmt.Printf("    Avg 1st-pass:   %s\n", fp)
	fmt.Printf("    Avg iterations: %s\n", ai)
	fmt.Printf("    Total tokens:   %dk in / %dk out\n", g.inputTokens/1000, g.outputTokens/1000)
	fmt.Printf("    Total duration: %.0f min\n", g.durationMin)
}

// randomWords generates a short random identifier like "swift-oak-river".
func randomWords() string {
	words := []string{
		"swift", "calm", "bold", "warm", "deep",
		"oak", "elm", "fox", "owl", "bee",
		"river", "stone", "cloud", "leaf", "dawn",
	}
	a := words[rand.Intn(5)]
	b := words[5+rand.Intn(5)]
	c := words[10+rand.Intn(5)]
	return fmt.Sprintf("%s-%s-%s", a, b, c)
}
