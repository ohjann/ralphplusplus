package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/eoghanhynes/ralph/internal/config"
	"github.com/eoghanhynes/ralph/internal/costs"
	"github.com/eoghanhynes/ralph/internal/memory"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/roles"
	"github.com/eoghanhynes/ralph/internal/runner"
)

// fakeEmbedder implements memory.Embedder for testing.
type fakeEmbedder struct {
	embedding []float64
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	result := make([][]float64, len(texts))
	for i := range texts {
		result[i] = f.embedding
	}
	return result, nil
}

func (f *fakeEmbedder) EmbedOne(_ context.Context, _ string) ([]float64, error) {
	return f.embedding, nil
}

// buildPromptOpts mirrors the logic in Run() that constructs BuildPromptOpts
// from worker fields and config. This is extracted to be independently testable
// since Run() has heavy side effects (workspace creation, Claude invocation, etc.).
func buildPromptOpts(w *Worker, cfg *config.Config) []runner.BuildPromptOpts {
	var opts []runner.BuildPromptOpts
	if w.ChromaClient != nil && w.Embedder != nil && !cfg.Memory.Disabled {
		retriever := memory.NewRetriever(w.ChromaClient, w.Embedder)
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
	return opts
}

// TestBuildPromptOpts_WithMemory verifies that when a Worker has ChromaClient
// and Embedder set, BuildPromptOpts are constructed with a valid retriever and
// the config's memory options.
func TestBuildPromptOpts_WithMemory(t *testing.T) {
	client := memory.NewClient("http://localhost:0")
	embedder := &fakeEmbedder{embedding: []float64{0.1, 0.2}}

	w := &Worker{
		ChromaClient: client,
		Embedder:     embedder,
	}
	cfg := &config.Config{
		Memory: config.MemoryConfig{
			TopK:      10,
			MinScore:  0.7,
			MaxTokens: 500,
		},
	}

	opts := buildPromptOpts(w, cfg)

	if len(opts) != 1 {
		t.Fatalf("expected 1 BuildPromptOpts, got %d", len(opts))
	}
	if opts[0].Memory == nil {
		t.Error("expected non-nil Memory retriever")
	}
	if opts[0].MemoryOpts.TopK != 10 {
		t.Errorf("TopK = %d, want 10", opts[0].MemoryOpts.TopK)
	}
	if opts[0].MemoryOpts.MinScore != 0.7 {
		t.Errorf("MinScore = %f, want 0.7", opts[0].MemoryOpts.MinScore)
	}
	if opts[0].MemoryOpts.MaxTokens != 500 {
		t.Errorf("MaxTokens = %d, want 500", opts[0].MemoryOpts.MaxTokens)
	}
}

// TestBuildPromptOpts_NilMemory verifies that workers with nil ChromaClient or
// Embedder produce no BuildPromptOpts (memory is disabled/unavailable).
func TestBuildPromptOpts_NilMemory(t *testing.T) {
	cfg := &config.Config{}

	tests := []struct {
		name   string
		worker *Worker
	}{
		{
			name:   "both nil",
			worker: &Worker{},
		},
		{
			name: "nil embedder",
			worker: &Worker{
				ChromaClient: memory.NewClient("http://localhost:0"),
			},
		},
		{
			name: "nil client",
			worker: &Worker{
				Embedder: &fakeEmbedder{embedding: []float64{0.1}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := buildPromptOpts(tt.worker, cfg)
			if len(opts) != 0 {
				t.Errorf("expected 0 BuildPromptOpts for %s, got %d", tt.name, len(opts))
			}
		})
	}
}

// TestBuildPromptOpts_MemoryDisabled verifies that even when ChromaClient and
// Embedder are set, no BuildPromptOpts are created if memory is disabled in config.
func TestBuildPromptOpts_MemoryDisabled(t *testing.T) {
	client := memory.NewClient("http://localhost:0")
	embedder := &fakeEmbedder{embedding: []float64{0.1, 0.2}}

	w := &Worker{
		ChromaClient: client,
		Embedder:     embedder,
	}
	cfg := &config.Config{
		Memory: config.MemoryConfig{
			Disabled: true,
			TopK:     10,
			MinScore: 0.7,
		},
	}

	opts := buildPromptOpts(w, cfg)

	if len(opts) != 0 {
		t.Errorf("expected 0 BuildPromptOpts when memory disabled, got %d", len(opts))
	}
}

func TestShouldRunArchitect(t *testing.T) {
	// Create a temp dir with a plan for the "has plan" test case
	tmpDir := t.TempDir()
	planDir := filepath.Join(tmpDir, ".ralph", "stories", "P4-001")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "plan.md"), []byte("existing plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	// PRD with a story that has a long description (>50 words)
	longDesc := "This is a long description that has more than fifty words in it. " +
		"We need to make sure that the architect phase runs for stories with substantial " +
		"descriptions that warrant a planning phase before implementation begins. " +
		"This description should be long enough to exceed the fifty word threshold easily now."
	testPRD := &prd.PRD{
		UserStories: []prd.UserStory{
			{ID: "P4-001", Title: "Test Story", Description: longDesc},
			{ID: "P4-002", Title: "Short Story", Description: "Short desc"},
		},
	}

	tests := []struct {
		name      string
		storyID   string
		iteration int
		wsDir     string
		prd       *prd.PRD
		want      bool
	}{
		{
			name:      "first iteration, long desc, no plan",
			storyID:   "P4-001",
			iteration: 1,
			wsDir:     t.TempDir(), // empty dir, no plan
			prd:       testPRD,
			want:      true,
		},
		{
			name:      "second iteration skips",
			storyID:   "P4-001",
			iteration: 2,
			wsDir:     t.TempDir(),
			prd:       testPRD,
			want:      false,
		},
		{
			name:      "FIX- story skips",
			storyID:   "FIX-001",
			iteration: 1,
			wsDir:     t.TempDir(),
			prd:       testPRD,
			want:      false,
		},
		{
			name:      "short description skips",
			storyID:   "P4-002",
			iteration: 1,
			wsDir:     t.TempDir(),
			prd:       testPRD,
			want:      false,
		},
		{
			name:      "plan already exists skips",
			storyID:   "P4-001",
			iteration: 1,
			wsDir:     tmpDir,
			prd:       testPRD,
			want:      false,
		},
		{
			name:      "nil PRD skips (no description)",
			storyID:   "P4-001",
			iteration: 1,
			wsDir:     t.TempDir(),
			prd:       nil,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRunArchitect(tt.storyID, tt.iteration, tt.wsDir, tt.prd)
			if got != tt.want {
				t.Errorf("shouldRunArchitect() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccumulateUsage(t *testing.T) {
	t.Run("both non-nil", func(t *testing.T) {
		a := &costs.TokenUsage{
			InputTokens:  100,
			OutputTokens: 50,
			CacheRead:    10,
			CacheWrite:   5,
			Model:        "claude-opus",
			Provider:     "claude",
			NumTurns:     3,
			DurationMS:   1000,
		}
		b := &costs.TokenUsage{
			InputTokens:  200,
			OutputTokens: 100,
			CacheRead:    20,
			CacheWrite:   10,
			Model:        "claude-opus",
			Provider:     "claude",
			NumTurns:     5,
			DurationMS:   2000,
		}

		result := accumulateUsage(a, b)
		if result.InputTokens != 300 {
			t.Errorf("InputTokens = %d, want 300", result.InputTokens)
		}
		if result.OutputTokens != 150 {
			t.Errorf("OutputTokens = %d, want 150", result.OutputTokens)
		}
		if result.CacheRead != 30 {
			t.Errorf("CacheRead = %d, want 30", result.CacheRead)
		}
		if result.CacheWrite != 15 {
			t.Errorf("CacheWrite = %d, want 15", result.CacheWrite)
		}
		if result.NumTurns != 8 {
			t.Errorf("NumTurns = %d, want 8", result.NumTurns)
		}
		if result.DurationMS != 3000 {
			t.Errorf("DurationMS = %d, want 3000", result.DurationMS)
		}
		if result.Model != "claude-opus" {
			t.Errorf("Model = %s, want claude-opus", result.Model)
		}
	})

	t.Run("first nil", func(t *testing.T) {
		b := &costs.TokenUsage{InputTokens: 100}
		result := accumulateUsage(nil, b)
		if result != b {
			t.Error("expected b returned when a is nil")
		}
	})

	t.Run("second nil", func(t *testing.T) {
		a := &costs.TokenUsage{InputTokens: 100}
		result := accumulateUsage(a, nil)
		if result != a {
			t.Error("expected a returned when b is nil")
		}
	})

	t.Run("both nil", func(t *testing.T) {
		result := accumulateUsage(nil, nil)
		if result != nil {
			t.Error("expected nil when both are nil")
		}
	})
}

func TestMakeBuildOpts(t *testing.T) {
	t.Run("with memory opts", func(t *testing.T) {
		memOpts := []runner.BuildPromptOpts{{
			MemoryOpts: memory.RetrievalOptions{TopK: 10},
		}}
		result := makeBuildOpts(memOpts, roles.RoleArchitect)
		if len(result) != 1 {
			t.Fatalf("expected 1 opt, got %d", len(result))
		}
		if result[0].Role != roles.RoleArchitect {
			t.Errorf("Role = %s, want %s", result[0].Role, roles.RoleArchitect)
		}
		if result[0].MemoryOpts.TopK != 10 {
			t.Errorf("TopK = %d, want 10", result[0].MemoryOpts.TopK)
		}
		// Original should not be mutated
		if memOpts[0].Role != "" {
			t.Error("original memoryOpts was mutated")
		}
	})

	t.Run("without memory opts", func(t *testing.T) {
		result := makeBuildOpts(nil, roles.RoleImplementer)
		if len(result) != 1 {
			t.Fatalf("expected 1 opt, got %d", len(result))
		}
		if result[0].Role != roles.RoleImplementer {
			t.Errorf("Role = %s, want %s", result[0].Role, roles.RoleImplementer)
		}
	})
}

func TestWorkerUpdateHasRoleField(t *testing.T) {
	// Verify WorkerUpdate carries the Role field for TUI display
	update := WorkerUpdate{
		WorkerID: 1,
		StoryID:  "P4-005",
		State:    WorkerRunning,
		Role:     roles.RoleArchitect,
	}
	if update.Role != roles.RoleArchitect {
		t.Errorf("Role = %s, want %s", update.Role, roles.RoleArchitect)
	}
}

func TestDebuggerRoleWhenStuckInWorker(t *testing.T) {
	// Verify that HasStuckInfo drives debugger role selection
	dir := t.TempDir()
	ralphDir := filepath.Join(dir, ".ralph")
	_ = os.MkdirAll(ralphDir, 0o755)

	// No stuck info — implementer role
	implRole := roles.RoleImplementer
	if runner.HasStuckInfo(dir, "P4-001") {
		implRole = roles.RoleDebugger
	}
	if implRole != roles.RoleImplementer {
		t.Errorf("expected RoleImplementer without stuck info, got %s", implRole)
	}

	// Write stuck info
	stuckData := []byte(`{"pattern":"repeated_bash_command","repeated_commands":["make test"],"count":5,"iteration":3,"story_id":"P4-001"}`)
	_ = os.WriteFile(filepath.Join(ralphDir, "stuck-3.json"), stuckData, 0o644)

	// With stuck info — debugger role
	implRole = roles.RoleImplementer
	if runner.HasStuckInfo(dir, "P4-001") {
		implRole = roles.RoleDebugger
	}
	if implRole != roles.RoleDebugger {
		t.Errorf("expected RoleDebugger with stuck info, got %s", implRole)
	}

	// makeBuildOpts should work with debugger role
	opts := makeBuildOpts(nil, implRole)
	if len(opts) != 1 {
		t.Fatalf("expected 1 opt, got %d", len(opts))
	}
	if opts[0].Role != roles.RoleDebugger {
		t.Errorf("expected RoleDebugger in BuildPromptOpts, got %s", opts[0].Role)
	}
}
