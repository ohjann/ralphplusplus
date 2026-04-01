package storystate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Status values for StoryState.
const (
	StatusInProgress       = "in_progress"
	StatusBlocked          = "blocked"
	StatusContextExhausted = "context_exhausted"
	StatusComplete         = "complete"
	StatusFailed           = "failed"
)

// Subtask represents a unit of work within a story.
type Subtask struct {
	Description string `json:"description"`
	Done        bool   `json:"done"`
}

// ErrorEntry records an error and its resolution.
type ErrorEntry struct {
	Error      string `json:"error"`
	Resolution string `json:"resolution"`
}

// StoryState represents the persisted state of a story's execution.
type StoryState struct {
	StoryID           string       `json:"story_id"`
	Status            string       `json:"status"`
	IterationCount    int          `json:"iteration_count"`
	FilesTouched      []string     `json:"files_touched"`
	Subtasks          []Subtask    `json:"subtasks"`
	ErrorsEncountered []ErrorEntry `json:"errors_encountered"`
	JudgeFeedback     []string     `json:"judge_feedback"`
	LastUpdated       time.Time    `json:"last_updated"`
	SessionID         string       `json:"session_id,omitempty"`
}

// storyDir returns the path to a story's state directory.
func storyDir(projectDir, storyID string) string {
	return filepath.Join(projectDir, ".ralph", "stories", storyID)
}

// Save writes the StoryState to .ralph/stories/{story_id}/state.json,
// creating directories as needed.
func Save(projectDir string, state StoryState) error {
	dir := storyDir(projectDir, state.StoryID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "state.json"), data, 0o644)
}

// Load reads and parses state.json for the given story ID.
// Returns a zero-value StoryState with no error if the file doesn't exist.
func Load(projectDir, storyID string) (StoryState, error) {
	path := filepath.Join(storyDir(projectDir, storyID), "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return StoryState{}, nil
		}
		return StoryState{}, err
	}
	var state StoryState
	if err := json.Unmarshal(data, &state); err != nil {
		return StoryState{}, err
	}
	return state, nil
}

// LoadPlan reads plan.md contents for the given story ID.
// Returns an empty string if the file doesn't exist.
func LoadPlan(projectDir, storyID string) (string, error) {
	return loadOptionalFile(projectDir, storyID, "plan.md")
}

// LoadDecisions reads decisions.md contents for the given story ID.
// Returns an empty string if the file doesn't exist.
func LoadDecisions(projectDir, storyID string) (string, error) {
	return loadOptionalFile(projectDir, storyID, "decisions.md")
}

// LoadHint reads hint.md contents for the given story ID.
// Returns an empty string if the file doesn't exist.
func LoadHint(projectDir, storyID string) (string, error) {
	return loadOptionalFile(projectDir, storyID, "hint.md")
}

// SaveHint writes a user hint to .ralph/stories/{story_id}/hint.md.
func SaveHint(projectDir, storyID, hint string) error {
	dir := storyDir(projectDir, storyID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "hint.md"), []byte(hint), 0o644)
}

// ClearHint removes the hint file after it has been consumed.
func ClearHint(projectDir, storyID string) {
	_ = os.Remove(filepath.Join(storyDir(projectDir, storyID), "hint.md"))
}

// loadOptionalFile reads a file from the story directory, returning empty string if missing.
func loadOptionalFile(projectDir, storyID, filename string) (string, error) {
	path := filepath.Join(storyDir(projectDir, storyID), filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}
