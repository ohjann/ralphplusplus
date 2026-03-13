package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/eoghanhynes/ralph/internal/config"
	"github.com/eoghanhynes/ralph/internal/costs"
	"github.com/eoghanhynes/ralph/internal/debuglog"
	"github.com/eoghanhynes/ralph/internal/memory"
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
		if err := printHistory(cfg.ProjectDir, cfg.HistoryAll); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Handle memory subcommand before validation (no prd.json needed).
	if cfg.MemoryCommand != "" {
		dataDir := filepath.Join(cfg.RalphHome, "memory")
		ctx := context.Background()
		var err error
		switch cfg.MemoryCommand {
		case "stats":
			err = memory.RunStats(ctx, dataDir, cfg.Memory.Port)
		case "search":
			err = memory.RunSearch(ctx, dataDir, cfg.Memory.Port, cfg.MemoryQuery)
		case "prune":
			err = memory.RunPrune(ctx, dataDir, cfg.Memory.Port)
		case "reset":
			err = memory.RunReset(ctx, dataDir, cfg.Memory.Port)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := cfg.EnsureDirs(); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directories: %v\n", err)
		os.Exit(1)
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
	fmt.Printf("%-19s  %-20s  %9s  %8s  %8s  %9s\n",
		"DATE", "PRD", "STORIES", "COST", "DURATION", "AVG ITER")
	fmt.Printf("%-19s  %-20s  %9s  %8s  %8s  %9s\n",
		"───────────────────", "────────────────────", "─────────", "────────", "────────", "─────────")

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

		fmt.Printf("%-19s  %-20s  %9s  %8s  %8s  %9s\n",
			date, prdName, stories, cost, duration, avgIter)
	}

	if !showAll && len(h.Runs) > 10 {
		fmt.Printf("\nShowing last 10 of %d runs. Use --all to see everything.\n", len(h.Runs))
	}
	return nil
}
