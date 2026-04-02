package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/prd"
	"github.com/ohjann/ralphplusplus/internal/roles"
	"github.com/ohjann/ralphplusplus/internal/runner"
)

func TestNeedsArchitect(t *testing.T) {
	// Create a temp dir for story state
	tmpDir := t.TempDir()

	longDesc := strings.Repeat("word ", 60) // 60 words

	tests := []struct {
		name     string
		storyID  string
		story    *prd.UserStory
		planData string // if non-empty, write plan.md before test
		want     bool
	}{
		{
			name:    "nil story",
			storyID: "P4-001",
			story:   nil,
			want:    false,
		},
		{
			name:    "FIX- story always skips architect",
			storyID: "FIX-001",
			story:   &prd.UserStory{ID: "FIX-001", Description: longDesc},
			want:    false,
		},
		{
			name:    "short description skips architect",
			storyID: "P4-001",
			story:   &prd.UserStory{ID: "P4-001", Description: "Fix the bug"},
			want:    false,
		},
		{
			name:    "long description needs architect",
			storyID: "P4-001",
			story:   &prd.UserStory{ID: "P4-001", Description: longDesc},
			want:    true,
		},
		{
			name:     "existing plan skips architect",
			storyID:  "P4-002",
			story:    &prd.UserStory{ID: "P4-002", Description: longDesc},
			planData: strings.Repeat("x", 60), // >= 50 bytes
			want:     false,
		},
		{
			name:     "existing but too-short plan still needs architect",
			storyID:  "P4-003",
			story:    &prd.UserStory{ID: "P4-003", Description: longDesc},
			planData: "tiny",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.planData != "" {
				storyDir := filepath.Join(tmpDir, ".ralph", "stories", tt.storyID)
				_ = os.MkdirAll(storyDir, 0o755)
				_ = os.WriteFile(filepath.Join(storyDir, "plan.md"), []byte(tt.planData), 0o644)
			}

			got := needsArchitect(tmpDir, tt.storyID, tt.story)
			if got != tt.want {
				t.Errorf("needsArchitect(%q) = %v, want %v", tt.storyID, got, tt.want)
			}
		})
	}
}

func TestCombineTokenUsage(t *testing.T) {
	a := &costs.TokenUsage{
		InputTokens:  100,
		OutputTokens: 200,
		CacheRead:    10,
		CacheWrite:   20,
		Model:        "architect-model",
		Provider:     "claude",
		NumTurns:     5,
		DurationMS:   1000,
	}
	b := &costs.TokenUsage{
		InputTokens:  300,
		OutputTokens: 400,
		CacheRead:    30,
		CacheWrite:   40,
		Model:        "implementer-model",
		Provider:     "claude",
		NumTurns:     10,
		DurationMS:   2000,
	}

	result := costs.CombineUsage(a, b)
	if result.InputTokens != 400 {
		t.Errorf("InputTokens = %d, want 400", result.InputTokens)
	}
	if result.OutputTokens != 600 {
		t.Errorf("OutputTokens = %d, want 600", result.OutputTokens)
	}
	if result.NumTurns != 15 {
		t.Errorf("NumTurns = %d, want 15", result.NumTurns)
	}
	if result.DurationMS != 3000 {
		t.Errorf("DurationMS = %d, want 3000", result.DurationMS)
	}
	if result.Model != "implementer-model" {
		t.Errorf("Model = %q, want implementer-model", result.Model)
	}

	// Test nil handling
	if costs.CombineUsage(nil, b) != b {
		t.Error("costs.CombineUsage(nil, b) should return b")
	}
	if costs.CombineUsage(a, nil) != a {
		t.Error("costs.CombineUsage(a, nil) should return a")
	}
	if costs.CombineUsage(nil, nil) != nil {
		t.Error("costs.CombineUsage(nil, nil) should return nil")
	}
}

func TestDebuggerRoleSelectedWhenStuck(t *testing.T) {
	dir := t.TempDir()
	ralphDir := filepath.Join(dir, ".ralph")
	_ = os.MkdirAll(ralphDir, 0o755)

	// No stuck info — should use implementer
	if runner.HasStuckInfo(dir, "P4-001") {
		t.Error("expected no stuck info initially")
	}

	// Write stuck info for the story
	info := runner.StuckInfo{
		Pattern:   "repeated_bash_command",
		Commands:  []string{"make test"},
		Count:     5,
		Iteration: 3,
		StoryID:   "P4-001",
	}
	data, _ := json.Marshal(info)
	_ = os.WriteFile(filepath.Join(ralphDir, "stuck-3.json"), data, 0o644)

	// Now should detect stuck info — debugger role should be selected
	if !runner.HasStuckInfo(dir, "P4-001") {
		t.Error("expected stuck info to be detected")
	}

	// Verify role selection logic
	implRole := roles.RoleImplementer
	if runner.HasStuckInfo(dir, "P4-001") {
		implRole = roles.RoleDebugger
	}
	if implRole != roles.RoleDebugger {
		t.Errorf("expected RoleDebugger, got %s", implRole)
	}

	// Different story should not trigger debugger
	implRole2 := roles.RoleImplementer
	if runner.HasStuckInfo(dir, "P4-999") {
		implRole2 = roles.RoleDebugger
	}
	if implRole2 != roles.RoleImplementer {
		t.Errorf("expected RoleImplementer for non-stuck story, got %s", implRole2)
	}
}
