package interactive

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eoghanhynes/ralph/internal/prd"
)

// SessionTask records an interactive task's state for the session file.
type SessionTask struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	Description      string `json:"description"`
	ClarificationQA  string `json:"clarification_qa,omitempty"`
	Completed        bool   `json:"completed"`
}

// SessionFile is the JSON structure saved to .ralph/session-{timestamp}.json.
type SessionFile struct {
	Timestamp string        `json:"timestamp"`
	Tasks     []SessionTask `json:"tasks"`
}

// SaveSession writes the interactive tasks from the PRD to a session file.
// Interactive tasks are identified by the "T-" prefix on their ID.
// Returns the path of the saved file, or empty string if no interactive tasks exist.
func SaveSession(projectDir string, stories []prd.UserStory) (string, error) {
	var tasks []SessionTask
	for _, s := range stories {
		if !strings.HasPrefix(s.ID, "T-") {
			continue
		}
		task := SessionTask{
			ID:        s.ID,
			Title:     s.Title,
			Completed: s.Passes,
		}
		// Split description back into task text and clarification Q&A
		if idx := strings.Index(s.Description, "\n\n## Clarification Q&A\n"); idx >= 0 {
			task.Description = s.Description[:idx]
			task.ClarificationQA = s.Description[idx+len("\n\n## Clarification Q&A\n"):]
		} else {
			task.Description = s.Description
		}
		tasks = append(tasks, task)
	}

	if len(tasks) == 0 {
		return "", nil
	}

	now := time.Now().UTC()
	sf := SessionFile{
		Timestamp: now.Format(time.RFC3339),
		Tasks:     tasks,
	}

	ralphDir := filepath.Join(projectDir, ".ralph")
	if err := os.MkdirAll(ralphDir, 0o755); err != nil {
		return "", fmt.Errorf("creating .ralph dir: %w", err)
	}

	filename := fmt.Sprintf("session-%s.json", now.Format("20060102-150405"))
	path := filepath.Join(ralphDir, filename)

	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling session file: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("writing session file: %w", err)
	}

	return path, nil
}
