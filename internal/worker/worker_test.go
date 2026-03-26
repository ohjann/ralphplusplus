package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eoghanhynes/ralph/internal/costs"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/roles"
	"github.com/eoghanhynes/ralph/internal/runner"
)

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
}
