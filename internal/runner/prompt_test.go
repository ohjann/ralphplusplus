package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/roles"
	"github.com/eoghanhynes/ralph/internal/storystate"
)

func TestBuildStoryStateContextIncludesHint(t *testing.T) {
	dir := t.TempDir()
	storyID := "TEST-001"

	// Create story state so buildStoryStateContext returns content
	state := storystate.StoryState{
		StoryID:        storyID,
		Status:         storystate.StatusInProgress,
		IterationCount: 2,
		LastUpdated:    time.Now(),
	}
	if err := storystate.Save(dir, state); err != nil {
		t.Fatalf("Save state: %v", err)
	}

	// Save a hint
	hintText := "Use the existing fetchUser helper"
	if err := storystate.SaveHint(dir, storyID, hintText); err != nil {
		t.Fatalf("SaveHint: %v", err)
	}

	// Build context — should include hint
	ctx := buildStoryStateContext(dir, storyID)
	if !strings.Contains(ctx, "### User Hint") {
		t.Error("expected '### User Hint' section in context")
	}
	if !strings.Contains(ctx, hintText) {
		t.Errorf("expected hint text %q in context, got:\n%s", hintText, ctx)
	}

	// Hint should be consumed (cleared after read)
	hint, _ := storystate.LoadHint(dir, storyID)
	if hint != "" {
		t.Errorf("hint should be cleared after buildStoryStateContext, got %q", hint)
	}
}

func TestBuildStoryStateContextNoHint(t *testing.T) {
	dir := t.TempDir()
	storyID := "TEST-002"

	state := storystate.StoryState{
		StoryID:        storyID,
		Status:         storystate.StatusInProgress,
		IterationCount: 1,
		LastUpdated:    time.Now(),
	}
	if err := storystate.Save(dir, state); err != nil {
		t.Fatalf("Save state: %v", err)
	}

	ctx := buildStoryStateContext(dir, storyID)
	if strings.Contains(ctx, "User Hint") {
		t.Error("should not contain User Hint section when no hint exists")
	}
}

func TestBuildStoryStateContextEmptyStateReturnsEmpty(t *testing.T) {
	dir := t.TempDir()

	// Even if hint exists, no state means empty return (early exit on IterationCount == 0)
	storyDir := filepath.Join(dir, ".ralph", "stories", "TEST-003")
	_ = os.MkdirAll(storyDir, 0o755)
	_ = os.WriteFile(filepath.Join(storyDir, "hint.md"), []byte("some hint"), 0o644)

	ctx := buildStoryStateContext(dir, "TEST-003")
	if ctx != "" {
		t.Errorf("expected empty context for story with no state, got %q", ctx)
	}
}

func TestBuildPromptWithPRDIncludesHint(t *testing.T) {
	dir := t.TempDir()
	ralphHome := t.TempDir()
	storyID := "TEST-004"

	// Create minimal ralph-prompt.md
	if err := os.WriteFile(filepath.Join(ralphHome, "ralph-prompt.md"), []byte("base prompt"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create story state
	state := storystate.StoryState{
		StoryID:        storyID,
		Status:         storystate.StatusInProgress,
		IterationCount: 1,
		LastUpdated:    time.Now(),
	}
	if err := storystate.Save(dir, state); err != nil {
		t.Fatal(err)
	}

	// Save hint
	hintText := "try a different approach"
	if err := storystate.SaveHint(dir, storyID, hintText); err != nil {
		t.Fatal(err)
	}

	// Build with a PRD so buildStoryStateContext is called
	p := &prd.PRD{
		Project:    "test",
		BranchName: "test-branch",
		UserStories: []prd.UserStory{
			{ID: storyID, Title: "Test story", Priority: 1},
		},
	}

	prompt, _, err := BuildPrompt(ralphHome, dir, storyID, p)
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}

	if !strings.Contains(prompt, "### User Hint") {
		t.Error("BuildPrompt output should contain '### User Hint' section")
	}
	if !strings.Contains(prompt, hintText) {
		t.Errorf("BuildPrompt output should contain hint text %q", hintText)
	}

	// Hint consumed
	hint, _ := storystate.LoadHint(dir, storyID)
	if hint != "" {
		t.Errorf("hint should be consumed after BuildPrompt, got %q", hint)
	}
}

func TestBuildPromptNilPRDNoCrash(t *testing.T) {
	ralphHome := t.TempDir()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(ralphHome, "ralph-prompt.md"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Should not crash with nil PRD
	prompt, _, err := BuildPrompt(ralphHome, dir, "ANY-001", nil)
	if err != nil {
		t.Fatalf("BuildPrompt with nil PRD: %v", err)
	}
	if !strings.Contains(prompt, "base") {
		t.Error("prompt should contain base content")
	}
}

func TestBuildPromptWithArchitectRoleLoadsRolePrompt(t *testing.T) {
	ralphHome := t.TempDir()
	dir := t.TempDir()

	// Create prompts directory and architect.md
	promptsDir := filepath.Join(ralphHome, "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "architect.md"), []byte("architect prompt template"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Also create ralph-prompt.md to prove it's NOT loaded
	if err := os.WriteFile(filepath.Join(ralphHome, "ralph-prompt.md"), []byte("default prompt template"), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt, _, err := BuildPrompt(ralphHome, dir, "TEST-010", nil, BuildPromptOpts{
		Role: roles.RoleArchitect,
	})
	if err != nil {
		t.Fatalf("BuildPrompt with architect role: %v", err)
	}
	if !strings.Contains(prompt, "architect prompt template") {
		t.Error("expected architect prompt template content")
	}
	if strings.Contains(prompt, "default prompt template") {
		t.Error("should NOT contain default ralph-prompt.md content")
	}
}

func TestBuildPromptArchitectSkipsIterationConstraint(t *testing.T) {
	ralphHome := t.TempDir()
	dir := t.TempDir()

	promptsDir := filepath.Join(ralphHome, "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "architect.md"), []byte("architect base"), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt, _, err := BuildPrompt(ralphHome, dir, "TEST-011", nil, BuildPromptOpts{
		Role: roles.RoleArchitect,
	})
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}
	if strings.Contains(prompt, "THIS ITERATION") {
		t.Error("architect role should NOT have THIS ITERATION constraint")
	}
}

func TestBuildPromptEmptyRoleUsesDefault(t *testing.T) {
	ralphHome := t.TempDir()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(ralphHome, "ralph-prompt.md"), []byte("default prompt"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Empty role — should behave exactly as before
	prompt, _, err := BuildPrompt(ralphHome, dir, "TEST-012", nil, BuildPromptOpts{})
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}
	if !strings.Contains(prompt, "default prompt") {
		t.Error("empty role should load ralph-prompt.md")
	}
	if strings.Contains(prompt, "STUCK DETECTION") {
		t.Error("should not contain stuck detection for non-debugger role")
	}
}

func TestBuildPromptDebuggerInjectsStuckInfo(t *testing.T) {
	ralphHome := t.TempDir()
	dir := t.TempDir()

	promptsDir := filepath.Join(ralphHome, "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "debugger.md"), []byte("debugger base"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create stuck info file
	ralphDir := filepath.Join(dir, ".ralph")
	if err := os.MkdirAll(ralphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stuckInfo := StuckInfo{
		Pattern:   "repeated_bash_command",
		Commands:  []string{"make test"},
		Count:     5,
		Iteration: 3,
		StoryID:   "TEST-013",
	}
	data, _ := json.Marshal(stuckInfo)
	if err := os.WriteFile(filepath.Join(ralphDir, "stuck-3.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	prompt, _, err := BuildPrompt(ralphHome, dir, "TEST-013", nil, BuildPromptOpts{
		Role: roles.RoleDebugger,
	})
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}
	if !strings.Contains(prompt, "STUCK DETECTION INFO") {
		t.Error("debugger role should include stuck detection info")
	}
	if !strings.Contains(prompt, "repeated_bash_command") {
		t.Error("should contain the stuck pattern")
	}
	if !strings.Contains(prompt, "make test") {
		t.Error("should contain the repeated command")
	}
}

func TestBuildPromptImplementerHasIterationConstraint(t *testing.T) {
	ralphHome := t.TempDir()
	dir := t.TempDir()

	promptsDir := filepath.Join(ralphHome, "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "implementer.md"), []byte("implementer base"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &prd.PRD{
		Project:    "test",
		BranchName: "test-branch",
		UserStories: []prd.UserStory{
			{ID: "TEST-014", Title: "Test story", Priority: 1},
		},
	}

	prompt, _, err := BuildPrompt(ralphHome, dir, "TEST-014", p, BuildPromptOpts{
		Role: roles.RoleImplementer,
	})
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}
	if !strings.Contains(prompt, "THIS ITERATION") {
		t.Error("implementer role should have THIS ITERATION constraint")
	}
}

func TestHasStuckInfo(t *testing.T) {
	dir := t.TempDir()
	ralphDir := filepath.Join(dir, ".ralph")

	// No .ralph dir — should return false
	if HasStuckInfo(dir, "TEST-001") {
		t.Error("expected false when no .ralph dir")
	}

	// Create .ralph dir but no stuck files
	_ = os.MkdirAll(ralphDir, 0o755)
	if HasStuckInfo(dir, "TEST-001") {
		t.Error("expected false when no stuck files")
	}

	// Create stuck file for different story
	info := StuckInfo{Pattern: "test", StoryID: "OTHER-001", Iteration: 1, Count: 3}
	data, _ := json.Marshal(info)
	_ = os.WriteFile(filepath.Join(ralphDir, "stuck-1.json"), data, 0o644)
	if HasStuckInfo(dir, "TEST-001") {
		t.Error("expected false when stuck file is for different story")
	}

	// Create stuck file for matching story
	info2 := StuckInfo{Pattern: "loop", StoryID: "TEST-001", Iteration: 2, Count: 5}
	data2, _ := json.Marshal(info2)
	_ = os.WriteFile(filepath.Join(ralphDir, "stuck-2.json"), data2, 0o644)
	if !HasStuckInfo(dir, "TEST-001") {
		t.Error("expected true when stuck file matches story")
	}

	// Stuck file with empty story ID should match any story
	dir2 := t.TempDir()
	ralphDir2 := filepath.Join(dir2, ".ralph")
	_ = os.MkdirAll(ralphDir2, 0o755)
	info3 := StuckInfo{Pattern: "generic", StoryID: "", Iteration: 1, Count: 2}
	data3, _ := json.Marshal(info3)
	_ = os.WriteFile(filepath.Join(ralphDir2, "stuck-1.json"), data3, 0o644)
	if !HasStuckInfo(dir2, "ANY-STORY") {
		t.Error("expected true when stuck file has empty story ID")
	}
}

func TestBuildDebuggerStuckContextIncludesErrors(t *testing.T) {
	dir := t.TempDir()
	ralphHome := t.TempDir()

	// Create debugger prompt
	promptsDir := filepath.Join(ralphHome, "prompts")
	_ = os.MkdirAll(promptsDir, 0o755)
	_ = os.WriteFile(filepath.Join(promptsDir, "debugger.md"), []byte("debugger base"), 0o644)

	// Create stuck info
	ralphDir := filepath.Join(dir, ".ralph")
	_ = os.MkdirAll(ralphDir, 0o755)
	stuckInfo := StuckInfo{Pattern: "repeated_test", StoryID: "TEST-020", Iteration: 2, Count: 4, Commands: []string{"make test"}}
	data, _ := json.Marshal(stuckInfo)
	_ = os.WriteFile(filepath.Join(ralphDir, "stuck-2.json"), data, 0o644)

	// Create state.json with errors_encountered
	storyDir := filepath.Join(dir, ".ralph", "stories", "TEST-020")
	_ = os.MkdirAll(storyDir, 0o755)
	state := storystate.StoryState{
		StoryID:        "TEST-020",
		Status:         "in_progress",
		IterationCount: 2,
		ErrorsEncountered: []storystate.ErrorEntry{
			{Error: "type mismatch on Foo", Resolution: "changed to Bar"},
			{Error: "nil pointer in handler", Resolution: ""},
		},
		LastUpdated: time.Now(),
	}
	stateData, _ := json.Marshal(state)
	_ = os.WriteFile(filepath.Join(storyDir, "state.json"), stateData, 0o644)

	// Build prompt with debugger role
	prompt, _, err := BuildPrompt(ralphHome, dir, "TEST-020", nil, BuildPromptOpts{
		Role: roles.RoleDebugger,
	})
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}

	if !strings.Contains(prompt, "STUCK DETECTION INFO") {
		t.Error("should contain stuck detection info section")
	}
	if !strings.Contains(prompt, "repeated_test") {
		t.Error("should contain the stuck pattern")
	}
	if !strings.Contains(prompt, "ERRORS ENCOUNTERED") {
		t.Error("should contain errors encountered section")
	}
	if !strings.Contains(prompt, "type mismatch on Foo") {
		t.Error("should contain error details")
	}
	if !strings.Contains(prompt, "changed to Bar") {
		t.Error("should contain error resolution")
	}
	if !strings.Contains(prompt, "nil pointer in handler") {
		t.Error("should contain second error")
	}
}
