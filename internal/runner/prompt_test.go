package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ohjann/ralphplusplus/internal/memory"
	"github.com/ohjann/ralphplusplus/internal/prd"
	"github.com/ohjann/ralphplusplus/internal/roles"
	"github.com/ohjann/ralphplusplus/internal/storystate"
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

	parts, err := BuildPrompt(ralphHome, dir, storyID, p)
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}

	if !strings.Contains(parts.UserMessage, "### User Hint") {
		t.Error("BuildPrompt output should contain '### User Hint' section")
	}
	if !strings.Contains(parts.UserMessage, hintText) {
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
	parts, err := BuildPrompt(ralphHome, dir, "ANY-001", nil)
	if err != nil {
		t.Fatalf("BuildPrompt with nil PRD: %v", err)
	}
	if !strings.Contains(parts.SystemAppend, "base") {
		t.Error("prompt should contain base content")
	}
}

func TestBuildPromptWithArchitectRoleLoadsRolePrompt(t *testing.T) {
	ralphHome := t.TempDir()
	dir := t.TempDir()
	t.Setenv("RALPH_HOME", ralphHome)

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

	parts, err := BuildPrompt(ralphHome, dir, "TEST-010", nil, BuildPromptOpts{
		Role: roles.RoleArchitect,
	})
	if err != nil {
		t.Fatalf("BuildPrompt with architect role: %v", err)
	}
	if !strings.Contains(parts.SystemAppend, "architect prompt template") {
		t.Error("expected architect prompt template content")
	}
	if strings.Contains(parts.SystemAppend, "default prompt template") {
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

	parts, err := BuildPrompt(ralphHome, dir, "TEST-011", nil, BuildPromptOpts{
		Role: roles.RoleArchitect,
	})
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}
	if strings.Contains(parts.UserMessage, "THIS ITERATION") {
		t.Error("architect role should NOT have THIS ITERATION constraint")
	}
}

func TestBuildPromptEmptyRoleUsesDefault(t *testing.T) {
	ralphHome := t.TempDir()
	dir := t.TempDir()
	t.Setenv("RALPH_HOME", ralphHome)

	if err := os.WriteFile(filepath.Join(ralphHome, "ralph-prompt.md"), []byte("default prompt"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Empty role — should behave exactly as before
	parts, err := BuildPrompt(ralphHome, dir, "TEST-012", nil, BuildPromptOpts{})
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}
	if !strings.Contains(parts.SystemAppend, "default prompt") {
		t.Error("empty role should load ralph-prompt.md")
	}
	if strings.Contains(parts.UserMessage, "STUCK DETECTION") {
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

	parts, err := BuildPrompt(ralphHome, dir, "TEST-013", nil, BuildPromptOpts{
		Role: roles.RoleDebugger,
	})
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}
	if !strings.Contains(parts.UserMessage, "STUCK DETECTION INFO") {
		t.Error("debugger role should include stuck detection info")
	}
	if !strings.Contains(parts.UserMessage, "repeated_bash_command") {
		t.Error("should contain the stuck pattern")
	}
	if !strings.Contains(parts.UserMessage, "make test") {
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

	parts, err := BuildPrompt(ralphHome, dir, "TEST-014", p, BuildPromptOpts{
		Role: roles.RoleImplementer,
	})
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}
	if !strings.Contains(parts.UserMessage, "THIS ITERATION") {
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
	parts, err := BuildPrompt(ralphHome, dir, "TEST-020", nil, BuildPromptOpts{
		Role: roles.RoleDebugger,
	})
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}

	if !strings.Contains(parts.UserMessage, "STUCK DETECTION INFO") {
		t.Error("should contain stuck detection info section")
	}
	if !strings.Contains(parts.UserMessage, "repeated_test") {
		t.Error("should contain the stuck pattern")
	}
	if !strings.Contains(parts.UserMessage, "ERRORS ENCOUNTERED") {
		t.Error("should contain errors encountered section")
	}
	if !strings.Contains(parts.UserMessage, "type mismatch on Foo") {
		t.Error("should contain error details")
	}
	if !strings.Contains(parts.UserMessage, "changed to Bar") {
		t.Error("should contain error resolution")
	}
	if !strings.Contains(parts.UserMessage, "nil pointer in handler") {
		t.Error("should contain second error")
	}
}

func TestBuildAntiPatternWarnings(t *testing.T) {
	patterns := []memory.AntiPattern{
		{
			Category:        "fragile_area",
			Description:     "File appears in 5 error documents",
			FilesAffected:   []string{"internal/runner/runner.go"},
			OccurrenceCount: 5,
			AffectedStories: []string{"P1-001", "P1-002", "P1-003"},
		},
		{
			Category:        "high_friction",
			Description:     "File involved in 3 high-iteration stories",
			FilesAffected:   []string{"internal/tui/model.go"},
			OccurrenceCount: 3,
			AffectedStories: []string{"P2-001", "P2-002"},
		},
	}

	t.Run("matching files produce warnings", func(t *testing.T) {
		storyFiles := map[string]bool{"internal/runner/runner.go": true}
		result := buildAntiPatternWarnings(patterns, storyFiles)
		if !strings.Contains(result, "KNOWN ISSUE: internal/runner/runner.go has caused fragile_area in 3 stories") {
			t.Errorf("expected warning for runner.go, got:\n%s", result)
		}
	})

	t.Run("no matching files produce empty string", func(t *testing.T) {
		storyFiles := map[string]bool{"internal/other/file.go": true}
		result := buildAntiPatternWarnings(patterns, storyFiles)
		if result != "" {
			t.Errorf("expected empty string for non-matching files, got:\n%s", result)
		}
	})

	t.Run("empty story files produce empty string", func(t *testing.T) {
		result := buildAntiPatternWarnings(patterns, map[string]bool{})
		if result != "" {
			t.Errorf("expected empty string for empty story files, got:\n%s", result)
		}
	})

	t.Run("max 3 warnings", func(t *testing.T) {
		manyPatterns := []memory.AntiPattern{
			{Category: "a", Description: "desc1", FilesAffected: []string{"f1.go"}, AffectedStories: []string{"s1"}},
			{Category: "b", Description: "desc2", FilesAffected: []string{"f2.go"}, AffectedStories: []string{"s2"}},
			{Category: "c", Description: "desc3", FilesAffected: []string{"f3.go"}, AffectedStories: []string{"s3"}},
			{Category: "d", Description: "desc4", FilesAffected: []string{"f4.go"}, AffectedStories: []string{"s4"}},
		}
		storyFiles := map[string]bool{"f1.go": true, "f2.go": true, "f3.go": true, "f4.go": true}
		result := buildAntiPatternWarnings(manyPatterns, storyFiles)
		count := strings.Count(result, "KNOWN ISSUE:")
		if count != 3 {
			t.Errorf("expected max 3 warnings, got %d:\n%s", count, result)
		}
	})
}

func TestBuildPromptWithAntiPatterns(t *testing.T) {
	ralphHome := t.TempDir()
	dir := t.TempDir()
	storyID := "TEST-AP1"

	if err := os.WriteFile(filepath.Join(ralphHome, "ralph-prompt.md"), []byte("base prompt"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create story state with files_touched
	state := storystate.StoryState{
		StoryID:        storyID,
		Status:         storystate.StatusInProgress,
		IterationCount: 1,
		FilesTouched:   []string{"internal/runner/runner.go"},
		LastUpdated:    time.Now(),
	}
	if err := storystate.Save(dir, state); err != nil {
		t.Fatal(err)
	}

	p := &prd.PRD{
		Project:    "test",
		BranchName: "test-branch",
		UserStories: []prd.UserStory{
			{ID: storyID, Title: "Test story", Priority: 1},
		},
	}

	antiPatterns := []memory.AntiPattern{
		{
			Category:        "fragile_area",
			Description:     "File appears in 5 error documents",
			FilesAffected:   []string{"internal/runner/runner.go"},
			OccurrenceCount: 5,
			AffectedStories: []string{"P1-001", "P1-002"},
		},
	}

	parts, err := BuildPrompt(ralphHome, dir, storyID, p, BuildPromptOpts{
		AntiPatterns: antiPatterns,
	})
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}

	if !strings.Contains(parts.UserMessage, "KNOWN ISSUE") {
		t.Error("prompt should contain KNOWN ISSUE warning")
	}
	if !strings.Contains(parts.UserMessage, "internal/runner/runner.go has caused fragile_area") {
		t.Error("prompt should contain specific warning about runner.go")
	}

	// Warnings should appear before memory retrieval (which is at the end)
	knownIdx := strings.Index(parts.UserMessage, "KNOWN ISSUE")
	iterIdx := strings.Index(parts.UserMessage, "THIS ITERATION")
	if knownIdx > iterIdx {
		t.Error("anti-pattern warnings should appear before THIS ITERATION section")
	}
}

func TestBuildPromptNoAntiPatternMatchNoSection(t *testing.T) {
	ralphHome := t.TempDir()
	dir := t.TempDir()
	storyID := "TEST-AP2"

	if err := os.WriteFile(filepath.Join(ralphHome, "ralph-prompt.md"), []byte("base prompt"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &prd.PRD{
		Project:    "test",
		BranchName: "test-branch",
		UserStories: []prd.UserStory{
			{ID: storyID, Title: "Test story", Priority: 1},
		},
	}

	antiPatterns := []memory.AntiPattern{
		{
			Category:      "fragile_area",
			Description:   "unrelated file",
			FilesAffected: []string{"internal/other/other.go"},
		},
	}

	parts, err := BuildPrompt(ralphHome, dir, storyID, p, BuildPromptOpts{
		AntiPatterns: antiPatterns,
	})
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}

	if strings.Contains(parts.UserMessage, "KNOWN ISSUE") {
		t.Error("prompt should NOT contain KNOWN ISSUE when no anti-patterns match")
	}
}

func TestExtractFilePaths(t *testing.T) {
	text := "Modify internal/runner/runner.go BuildPrompt() to accept anti-patterns parameter"
	paths := extractFilePaths(text)
	found := false
	for _, p := range paths {
		if p == "internal/runner/runner.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected to extract internal/runner/runner.go from text, got %v", paths)
	}
}

func TestBuildPromptWithMemoryFilesIncludesLearnedContext(t *testing.T) {
	ralphHome := t.TempDir()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(ralphHome, "ralph-prompt.md"), []byte("base prompt"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create project-specific learnings in {projectDir}/.ralph/memory/
	projectMemDir := filepath.Join(dir, ".ralph", "memory")
	if err := os.MkdirAll(projectMemDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectMemDir, "learnings.md"), []byte("### L-001\nAlways run tests before committing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create global PRD learnings in {ralphHome}/memory/
	globalMemDir := filepath.Join(ralphHome, "memory")
	if err := os.MkdirAll(globalMemDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalMemDir, "prd-learnings.md"), []byte("### PL-001\nBreak large stories into subtasks\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	parts, err := BuildPrompt(ralphHome, dir, "TEST-MEM1", nil)
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}

	if !strings.Contains(parts.UserMessage, "## Learned Context (from previous runs)") {
		t.Error("expected Learned Context section in prompt")
	}
	if !strings.Contains(parts.UserMessage, "### Cross-Story Learnings") {
		t.Error("expected Cross-Story Learnings subsection")
	}
	if !strings.Contains(parts.UserMessage, "Always run tests before committing") {
		t.Error("expected learnings content in prompt")
	}
	if !strings.Contains(parts.UserMessage, "### PRD-Specific Learnings") {
		t.Error("expected PRD-Specific Learnings subsection")
	}
	if !strings.Contains(parts.UserMessage, "Break large stories into subtasks") {
		t.Error("expected prd-learnings content in prompt")
	}
}

func TestBuildPromptNoMemoryFilesOmitsSection(t *testing.T) {
	ralphHome := t.TempDir()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(ralphHome, "ralph-prompt.md"), []byte("base prompt"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No memory files exist
	parts, err := BuildPrompt(ralphHome, dir, "TEST-MEM2", nil)
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}

	if strings.Contains(parts.UserMessage, "Learned Context") {
		t.Error("prompt should NOT contain Learned Context when no memory files exist")
	}
}

func TestBuildPromptMemoryDisabledSkipsInjection(t *testing.T) {
	ralphHome := t.TempDir()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(ralphHome, "ralph-prompt.md"), []byte("base prompt"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create memory files
	memDir := filepath.Join(ralphHome, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "learnings.md"), []byte("### L-001\nSome learning\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	parts, err := BuildPrompt(ralphHome, dir, "TEST-MEM3", nil, BuildPromptOpts{MemoryDisabled: true})
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}

	if strings.Contains(parts.UserMessage, "Learned Context") {
		t.Error("prompt should NOT contain Learned Context when MemoryDisabled is true")
	}
}

func TestBuildPromptEmptyMemoryFilesOmitsSection(t *testing.T) {
	ralphHome := t.TempDir()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(ralphHome, "ralph-prompt.md"), []byte("base prompt"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create empty memory files
	memDir := filepath.Join(ralphHome, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "learnings.md"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "prd-learnings.md"), []byte("   \n  \n"), 0o644); err != nil {
		t.Fatal(err)
	}

	parts, err := BuildPrompt(ralphHome, dir, "TEST-MEM4", nil)
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}

	if strings.Contains(parts.UserMessage, "Learned Context") {
		t.Error("prompt should NOT contain Learned Context when memory files are empty/whitespace")
	}
}
