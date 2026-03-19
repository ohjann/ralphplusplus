package memory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/eoghanhynes/ralph/internal/events"
	"github.com/eoghanhynes/ralph/internal/storystate"
)

// Pipeline orchestrates the extraction, chunking, and embedding of story data
// into ChromaDB collections.
type Pipeline struct {
	Client   *ChromaClient
	Embedder Embedder
}

// NewPipeline creates a new Pipeline with the given ChromaDB client and embedder.
func NewPipeline(client *ChromaClient, embedder Embedder) *Pipeline {
	return &Pipeline{
		Client:   client,
		Embedder: embedder,
	}
}

// ProcessStoryCompletion extracts completion summary, files touched, patterns,
// errors, and decisions from story state and events, then embeds them into the
// appropriate ChromaDB collections.
func (p *Pipeline) ProcessStoryCompletion(ctx context.Context, projectDir, storyID string) error {
	return p.processStory(ctx, projectDir, storyID, "complete")
}

// ProcessContextExhaustion performs the same extraction as ProcessStoryCompletion
// but tags documents with status=context_exhausted in metadata.
func (p *Pipeline) ProcessContextExhaustion(ctx context.Context, projectDir, storyID string) error {
	return p.processStory(ctx, projectDir, storyID, "context_exhausted")
}

func (p *Pipeline) processStory(ctx context.Context, projectDir, storyID, status string) error {
	state, err := storystate.Load(projectDir, storyID)
	if err != nil {
		return fmt.Errorf("load story state: %w", err)
	}

	decisions, err := storystate.LoadDecisions(projectDir, storyID)
	if err != nil {
		return fmt.Errorf("load decisions: %w", err)
	}

	storyEvents, err := events.Query(projectDir, events.Filter{StoryID: storyID})
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}

	now := time.Now()

	// Extract and embed patterns from events
	if err := p.embedPatterns(ctx, storyID, storyEvents, now); err != nil {
		return fmt.Errorf("embed patterns: %w", err)
	}

	// Extract and embed story completion summary
	if err := p.embedCompletion(ctx, storyID, state, storyEvents, status, now); err != nil {
		return fmt.Errorf("embed completion: %w", err)
	}

	// Extract and embed errors with resolutions
	if err := p.embedErrors(ctx, storyID, state, now); err != nil {
		return fmt.Errorf("embed errors: %w", err)
	}

	// Extract and embed decisions
	if err := p.embedDecisions(ctx, storyID, decisions, now); err != nil {
		return fmt.Errorf("embed decisions: %w", err)
	}

	return nil
}

// embedPatterns extracts patterns from pattern events and embeds each as a
// separate document in ralph_patterns.
func (p *Pipeline) embedPatterns(ctx context.Context, storyID string, evts []events.Event, now time.Time) error {
	var docs []Document
	var texts []string

	for _, ev := range evts {
		if ev.Type != events.EventPattern {
			continue
		}
		for _, pattern := range ev.Patterns {
			pattern = strings.TrimSpace(pattern)
			if pattern == "" {
				continue
			}
			doc := Document{
				ID:      contentHash("pattern", storyID, pattern),
				Content: pattern,
				Metadata: map[string]interface{}{
					"story_id":        storyID,
					"timestamp":       ev.Timestamp.Format(time.RFC3339),
					"files_involved":  strings.Join(ev.Files, ","),
					"relevance_score": 1.0,
					"last_confirmed":  now.Format(time.RFC3339),
				},
			}
			docs = append(docs, doc)
			texts = append(texts, pattern)
		}
	}

	if len(docs) == 0 {
		return nil
	}

	embeddings, err := p.Embedder.Embed(ctx, texts)
	if err != nil {
		return err
	}
	for i := range docs {
		docs[i].Embedding = embeddings[i]
	}

	return DeduplicateInsertBatch(ctx, p.Client, CollectionPatterns.Name, docs)
}

// embedCompletion creates a summary document for the story completion and
// embeds it into ralph_completions.
func (p *Pipeline) embedCompletion(ctx context.Context, storyID string, state storystate.StoryState, evts []events.Event, status string, now time.Time) error {
	// Build a summary from available data
	var parts []string

	// Find story_complete or context_exhausted events for a summary
	for _, ev := range evts {
		if ev.Type == events.EventStoryComplete || ev.Type == events.EventContextExhausted {
			if ev.Summary != "" {
				parts = append(parts, ev.Summary)
			}
		}
	}

	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("Story %s %s", storyID, status))
	}

	filesTouched := strings.Join(state.FilesTouched, ", ")
	if filesTouched != "" {
		parts = append(parts, fmt.Sprintf("Files: %s", filesTouched))
	}

	content := strings.Join(parts, ". ")
	docID := contentHash("completion", storyID, content)

	embedding, err := p.Embedder.EmbedOne(ctx, content)
	if err != nil {
		return err
	}

	doc := Document{
		ID:        docID,
		Content:   content,
		Embedding: embedding,
		Metadata: map[string]interface{}{
			"story_id":        storyID,
			"status":          status,
			"files_touched":   strings.Join(state.FilesTouched, ","),
			"iteration_count": float64(state.IterationCount),
			"relevance_score": 1.0,
			"last_confirmed":  now.Format(time.RFC3339),
		},
	}

	return DeduplicateInsertBatch(ctx, p.Client, CollectionCompletions.Name, []Document{doc})
}

// embedErrors extracts error+resolution pairs from story state and embeds each
// as a separate document in ralph_errors.
func (p *Pipeline) embedErrors(ctx context.Context, storyID string, state storystate.StoryState, now time.Time) error {
	if len(state.ErrorsEncountered) == 0 {
		return nil
	}

	var docs []Document
	var texts []string

	for _, entry := range state.ErrorsEncountered {
		content := fmt.Sprintf("Error: %s\nResolution: %s", entry.Error, entry.Resolution)
		doc := Document{
			ID:      contentHash("error", storyID, content),
			Content: content,
			Metadata: map[string]interface{}{
				"story_id":        storyID,
				"error_type":      entry.Error,
				"resolution":      entry.Resolution,
				"relevance_score": 1.0,
				"last_confirmed":  now.Format(time.RFC3339),
			},
		}
		docs = append(docs, doc)
		texts = append(texts, content)
	}

	embeddings, err := p.Embedder.Embed(ctx, texts)
	if err != nil {
		return err
	}
	for i := range docs {
		docs[i].Embedding = embeddings[i]
	}

	return DeduplicateInsertBatch(ctx, p.Client, CollectionErrors.Name, docs)
}

// embedDecisions parses decisions.md into individual decision blocks and embeds
// each as a separate document in ralph_decisions.
func (p *Pipeline) embedDecisions(ctx context.Context, storyID, decisions string, now time.Time) error {
	if strings.TrimSpace(decisions) == "" {
		return nil
	}

	blocks := splitDecisions(decisions)
	if len(blocks) == 0 {
		return nil
	}

	var docs []Document
	var texts []string

	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		doc := Document{
			ID:      contentHash("decision", storyID, block),
			Content: block,
			Metadata: map[string]interface{}{
				"story_id":        storyID,
				"timestamp":       now.Format(time.RFC3339),
				"relevance_score": 1.0,
				"last_confirmed":  now.Format(time.RFC3339),
			},
		}
		docs = append(docs, doc)
		texts = append(texts, block)
	}

	if len(docs) == 0 {
		return nil
	}

	embeddings, err := p.Embedder.Embed(ctx, texts)
	if err != nil {
		return err
	}
	for i := range docs {
		docs[i].Embedding = embeddings[i]
	}

	return DeduplicateInsertBatch(ctx, p.Client, CollectionDecisions.Name, docs)
}

// splitDecisions splits a decisions.md file into individual decision blocks.
// It splits on markdown headings (## or ###) or double newlines as separators.
func splitDecisions(content string) []string {
	lines := strings.Split(content, "\n")
	var blocks []string
	var current []string

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") {
			if len(current) > 0 {
				block := strings.TrimSpace(strings.Join(current, "\n"))
				if block != "" {
					blocks = append(blocks, block)
				}
			}
			current = []string{line}
		} else {
			current = append(current, line)
		}
	}

	if len(current) > 0 {
		block := strings.TrimSpace(strings.Join(current, "\n"))
		if block != "" {
			blocks = append(blocks, block)
		}
	}

	// If no headings found, treat the whole content as one block
	if len(blocks) == 0 {
		trimmed := strings.TrimSpace(content)
		if trimmed != "" {
			blocks = append(blocks, trimmed)
		}
	}

	return blocks
}

// EmbedLessons embeds synthesized lessons into the ralph_lessons collection with
// deduplication. When a near-duplicate exists (>0.9 cosine similarity), the
// existing document's times_confirmed is incremented and confidence bumped by 0.1
// (capped at 1.0). After insertion, the collection cap of 100 is enforced.
// Also persists lessons to .ralph/lessons.json via SaveLessons.
func EmbedLessons(ctx context.Context, client *ChromaClient, embedder Embedder, lessons []Lesson, projectDir string) error {
	if len(lessons) == 0 {
		return nil
	}

	projectID := GenerateProjectID(projectDir)
	projectFilter := map[string]interface{}{"project_id": projectID}

	for _, lesson := range lessons {
		content := lesson.Pattern + "\n" + lesson.Recommendation
		embedding, err := embedder.EmbedOne(ctx, content)
		if err != nil {
			return fmt.Errorf("embed lesson %q: %w", lesson.ID, err)
		}

		doc := Document{
			ID:        contentHash("lesson", lesson.ID, content),
			Content:   content,
			Embedding: embedding,
			Metadata: map[string]interface{}{
				"category":        lesson.Category,
				"confidence":      lesson.Confidence,
				"times_confirmed": float64(lesson.TimesConfirmed),
				"relevance_score": lesson.Confidence,
				"project_id":      projectID,
			},
		}

		// Check for near-duplicate within the same project
		results, err := client.QueryCollectionFiltered(ctx, CollectionLessons.Name, embedding, 1, projectFilter)
		if err == nil && len(results) > 0 && results[0].Distance < 0.1 {
			// Near-duplicate found — increment times_confirmed and bump confidence
			existing := results[0].Document
			timesConfirmed := 1.0
			if tc, ok := existing.Metadata["times_confirmed"].(float64); ok {
				timesConfirmed = tc
			}
			timesConfirmed++

			confidence := lesson.Confidence
			if ec, ok := existing.Metadata["confidence"].(float64); ok {
				confidence = ec + 0.1
			}
			if confidence > 1.0 {
				confidence = 1.0
			}

			existing.Metadata["times_confirmed"] = timesConfirmed
			existing.Metadata["confidence"] = confidence
			existing.Metadata["relevance_score"] = confidence
			existing.Content = content
			existing.Embedding = embedding

			if err := client.UpdateDocument(ctx, CollectionLessons.Name, existing); err != nil {
				return fmt.Errorf("update duplicate lesson: %w", err)
			}
		} else {
			// No duplicate — insert new
			if err := client.AddDocuments(ctx, CollectionLessons.Name, []Document{doc}); err != nil {
				return fmt.Errorf("add lesson document: %w", err)
			}
		}
	}

	// Enforce collection cap
	if err := EnforceCollectionCap(ctx, client, CollectionLessons.Name, CollectionLessons.MaxDocuments); err != nil {
		return fmt.Errorf("enforce lessons cap: %w", err)
	}

	// Persist to .ralph/lessons.json
	existing, _ := LoadLessons(projectDir)
	existing.Lessons = append(existing.Lessons, lessons...)
	if err := SaveLessons(projectDir, existing); err != nil {
		return fmt.Errorf("save lessons: %w", err)
	}

	return nil
}

// contentHash generates a deterministic document ID based on content hash.
// This supports idempotent upserts — the same content always produces the same ID.
func contentHash(docType, storyID, content string) string {
	h := sha256.New()
	h.Write([]byte(docType))
	h.Write([]byte(":"))
	h.Write([]byte(storyID))
	h.Write([]byte(":"))
	h.Write([]byte(content))
	return fmt.Sprintf("%s-%s-%x", docType, storyID, h.Sum(nil)[:8])
}
