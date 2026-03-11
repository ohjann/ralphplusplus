package memory

import "time"

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
