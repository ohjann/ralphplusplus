package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Lesson represents a learned lesson from post-run synthesis.
// Categories include: testing, architecture, sizing, ordering, criteria, tooling.
type Lesson struct {
	ID             string    `json:"id"`
	Category       string    `json:"category"`
	Pattern        string    `json:"pattern"`
	Evidence       string    `json:"evidence"`
	Recommendation string    `json:"recommendation"`
	Confidence     float64   `json:"confidence"`
	TimesConfirmed int       `json:"times_confirmed"`
	CreatedAt      time.Time `json:"created_at"`
}

// LessonsFile is the top-level structure for .ralph/lessons.json.
type LessonsFile struct {
	Lessons    []Lesson `json:"lessons"`
	PRDLessons []Lesson `json:"prd_lessons,omitempty"`
}

// SaveLessons writes lessons to .ralph/lessons.json with indented JSON.
func SaveLessons(projectDir string, lessons LessonsFile) error {
	dir := filepath.Join(projectDir, ".ralph")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(lessons, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "lessons.json"), data, 0o644)
}

// LoadLessons reads .ralph/lessons.json. Returns an empty LessonsFile if the file doesn't exist.
func LoadLessons(projectDir string) (LessonsFile, error) {
	path := filepath.Join(projectDir, ".ralph", "lessons.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LessonsFile{}, nil
		}
		return LessonsFile{}, err
	}
	var lf LessonsFile
	if err := json.Unmarshal(data, &lf); err != nil {
		return LessonsFile{}, err
	}
	return lf, nil
}

// LearningEntry represents a single learning entry for markdown memory files.
type LearningEntry struct {
	ID        string
	Run       string
	Stories   []string
	Confirmed int
	Category  string
	Content   string
}

// AntiPattern represents a recurring failure pattern. Retained as a type
// for BuildPromptOpts but detection via ChromaDB is removed.
type AntiPattern struct {
	Category        string
	Description     string
	FilesAffected   []string
	OccurrenceCount int
	AffectedStories []string
}
