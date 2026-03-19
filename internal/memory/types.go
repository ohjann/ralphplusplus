package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Document represents a vector memory document stored in ChromaDB.
type Document struct {
	ID        string
	Content   string
	Embedding []float64
	Metadata  map[string]interface{}
}

// RelevanceScore returns the relevance_score from metadata, or 0 if not set.
func (d Document) RelevanceScore() float64 {
	if d.Metadata == nil {
		return 0
	}
	v, ok := d.Metadata["relevance_score"]
	if !ok {
		return 0
	}
	f, ok := v.(float64)
	if !ok {
		return 0
	}
	return f
}

// LastConfirmed returns the last_confirmed time from metadata, or zero time if not set.
func (d Document) LastConfirmed() time.Time {
	if d.Metadata == nil {
		return time.Time{}
	}
	v, ok := d.Metadata["last_confirmed"]
	if !ok {
		return time.Time{}
	}
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		parsed, err := time.Parse(time.RFC3339, t)
		if err != nil {
			return time.Time{}
		}
		return parsed
	default:
		return time.Time{}
	}
}

// StoryID returns the story_id from metadata, or empty string if not set.
func (d Document) StoryID() string {
	if d.Metadata == nil {
		return ""
	}
	v, _ := d.Metadata["story_id"].(string)
	return v
}

// Collection defines a ChromaDB collection with a name and max document limit.
type Collection struct {
	Name         string
	MaxDocuments int
}

// Predefined collections for ralph's semantic memory.
var (
	CollectionPatterns    = Collection{Name: "ralph_patterns", MaxDocuments: 100}
	CollectionCompletions = Collection{Name: "ralph_completions", MaxDocuments: 200}
	CollectionErrors     = Collection{Name: "ralph_errors", MaxDocuments: 100}
	CollectionDecisions  = Collection{Name: "ralph_decisions", MaxDocuments: 100}
	CollectionCodebase   = Collection{Name: "ralph_codebase", MaxDocuments: 300}
)

// AllCollections returns all predefined collections.
func AllCollections() []Collection {
	return []Collection{
		CollectionPatterns,
		CollectionCompletions,
		CollectionErrors,
		CollectionDecisions,
		CollectionCodebase,
	}
}

// QueryResult represents a single result from a ChromaDB query.
type QueryResult struct {
	Document Document
	Score    float64
	Distance float64
}

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
	Lessons []Lesson `json:"lessons"`
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
