package memory

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eoghanhynes/ralph/internal/config"
)

// RetrievalOptions controls how semantic retrieval behaves.
type RetrievalOptions struct {
	TopK      int     // Number of results per collection (default 5)
	MinScore  float64 // Minimum similarity score threshold (default 0.7)
	MaxTokens int     // Token budget for formatted output (default 2000)
	Disabled  bool    // If true, return empty string immediately
	RepoID    string  // If set, only retrieve documents from this repository
}

// DocRef identifies a document in a specific collection, with optional
// retrieval metadata for TUI display.
type DocRef struct {
	Collection string
	DocID      string
	Score      float64 // combined relevance score (0–1)
	Content    string  // content preview for TUI display
}

// RetrievalResult holds the formatted markdown and the document references
// that contributed to it, enabling confirmation tracking.
type RetrievalResult struct {
	Text       string
	DocRefs    []DocRef
	TotalFound int // total results that passed MinScore (before token budget trim)
	MaxTokens  int // token budget used for selection
}

// DefaultRetrievalOptions returns options derived from the default config.
func DefaultRetrievalOptions() RetrievalOptions {
	defaults := config.DefaultMemoryConfig()
	return RetrievalOptions{
		TopK:      defaults.TopK,
		MinScore:  defaults.MinScore,
		MaxTokens: defaults.MaxTokens,
	}
}

// rankedResult holds a query result with its combined score and source collection.
type rankedResult struct {
	result     QueryResult
	collection string
	combined   float64
}

// collectionSection maps collection names to their markdown section headers.
var collectionSection = map[string]string{
	CollectionPatterns.Name:    "### Relevant Patterns",
	CollectionCompletions.Name: "### Prior Completions",
	CollectionErrors.Name:     "### Known Errors",
	CollectionDecisions.Name:  "### Architectural Decisions",
	CollectionCodebase.Name:   "### Codebase Context",
	CollectionLessons.Name:    "### Cross-Story Lessons",
	CollectionPRDLessons.Name: "### PRD Quality Lessons",
}

// RetrieveContext queries all memory collections for content relevant to a story,
// ranks results by relevance and recency, applies a token budget, and formats
// the output as markdown for prompt injection. Returns both the formatted text
// and the document references that contributed to it.
func RetrieveContext(
	ctx context.Context,
	client *ChromaClient,
	embedder Embedder,
	storyTitle string,
	storyDescription string,
	acceptanceCriteria []string,
	opts RetrievalOptions,
) (RetrievalResult, error) {
	if opts.Disabled {
		return RetrievalResult{}, nil
	}

	if client == nil {
		return RetrievalResult{}, fmt.Errorf("chromadb client is nil")
	}
	if embedder == nil {
		return RetrievalResult{}, fmt.Errorf("embedder is nil")
	}

	// Apply defaults for zero values.
	defaults := DefaultRetrievalOptions()
	if opts.TopK <= 0 {
		opts.TopK = defaults.TopK
	}
	if opts.MinScore <= 0 {
		opts.MinScore = defaults.MinScore
	}
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = defaults.MaxTokens
	}

	// Build query string from story context.
	parts := []string{storyTitle}
	if storyDescription != "" {
		parts = append(parts, storyDescription)
	}
	parts = append(parts, acceptanceCriteria...)
	query := strings.Join(parts, " ")

	// Get embedding for the query.
	embedding, err := embedder.EmbedOne(ctx, query)
	if err != nil {
		return RetrievalResult{}, fmt.Errorf("embed query: %w", err)
	}

	// Build optional repo filter for scoped retrieval.
	var filter []QueryFilter
	if opts.RepoID != "" {
		filter = []QueryFilter{{Where: map[string]interface{}{"repo_id": opts.RepoID}}}
	}

	// Query all collections concurrently and gather results.
	collections := AllCollections()
	now := time.Now()

	type colResults struct {
		name    string
		results []QueryResult
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	allResults := make([]colResults, 0, len(collections))
	var queryErrors []error

	for _, col := range collections {
		wg.Add(1)
		go func(colName string) {
			defer wg.Done()
			results, err := client.QueryCollection(ctx, colName, embedding, opts.TopK, filter...)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				queryErrors = append(queryErrors, fmt.Errorf("collection %s: %w", colName, err))
				return
			}
			allResults = append(allResults, colResults{name: colName, results: results})
		}(col.Name)
	}
	wg.Wait()

	// If all collections failed, surface the errors.
	if len(allResults) == 0 && len(queryErrors) == len(collections) {
		return RetrievalResult{}, fmt.Errorf("all collection queries failed: %v", queryErrors[0])
	}

	ranked := make([]rankedResult, 0, len(collections)*opts.TopK)
	for _, cr := range allResults {
		for _, r := range cr.results {
			if r.Score < opts.MinScore {
				continue
			}
			recencyWeight := computeRecencyWeight(r.Document.LastConfirmed(), now)
			confidenceWeight := confidenceWeightForCollection(cr.name, r.Document)
			ranked = append(ranked, rankedResult{
				result:     r,
				collection: cr.name,
				combined:   r.Score * recencyWeight * confidenceWeight,
			})
		}
	}

	if len(ranked) == 0 {
		return RetrievalResult{}, nil
	}

	// Sort by combined score descending.
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].combined > ranked[j].combined
	})

	// Apply token budget: estimate tokens as len(content)/4.
	var selected []rankedResult
	tokenCount := 0
	for _, r := range ranked {
		contentTokens := estimateTokens(r.result.Document.Content)
		if tokenCount+contentTokens > opts.MaxTokens && len(selected) > 0 {
			break
		}
		selected = append(selected, r)
		tokenCount += contentTokens
	}

	docRefs := make([]DocRef, len(selected))
	for i, r := range selected {
		docRefs[i] = DocRef{
			Collection: r.collection,
			DocID:      r.result.Document.ID,
			Score:      r.combined,
			Content:    r.result.Document.Content,
		}
	}

	return RetrievalResult{
		Text:       formatMarkdown(selected),
		DocRefs:    docRefs,
		TotalFound: len(ranked),
		MaxTokens:  opts.MaxTokens,
	}, nil
}

// isLessonCollection returns true if the collection has confidence metadata.
func isLessonCollection(name string) bool {
	return name == CollectionLessons.Name || name == CollectionPRDLessons.Name
}

// confidenceWeightForCollection returns the confidence weight to apply in ranking.
// For lesson collections, returns min(confidence, 1.0) from document metadata.
// For other collections, returns 1.0 (no effect on ranking).
func confidenceWeightForCollection(collName string, doc Document) float64 {
	if !isLessonCollection(collName) {
		return 1.0
	}
	return doc.Confidence()
}

// computeRecencyWeight returns a decay factor based on how many days ago
// the document was last confirmed. Documents with no last_confirmed time
// get a weight of 0.5.
func computeRecencyWeight(lastConfirmed time.Time, now time.Time) float64 {
	if lastConfirmed.IsZero() {
		return 0.5
	}
	days := now.Sub(lastConfirmed).Hours() / 24
	if days < 0 {
		days = 0
	}
	// Exponential decay: half-life of 30 days, capped at 1.0.
	w := math.Exp(-0.693 * days / 30)
	if w > 1.0 {
		w = 1.0
	}
	return w
}

// estimateTokens provides a rough token count as len(content)/4.
func estimateTokens(content string) int {
	tokens := len(content) / 4
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// formatMarkdown groups selected results by collection and formats them
// as markdown sections.
func formatMarkdown(results []rankedResult) string {
	// Group by collection, preserving rank order within each group.
	groups := make(map[string][]rankedResult)
	var order []string
	for _, r := range results {
		if _, seen := groups[r.collection]; !seen {
			order = append(order, r.collection)
		}
		groups[r.collection] = append(groups[r.collection], r)
	}

	var sb strings.Builder
	sb.WriteString("## Relevant Memory\n\n")

	for i, col := range order {
		if i > 0 {
			sb.WriteString("\n")
		}
		header, ok := collectionSection[col]
		if !ok {
			header = "### " + col
		}
		sb.WriteString(header)
		sb.WriteString("\n\n")

		for _, r := range groups[col] {
			sb.WriteString(fmt.Sprintf("- %s (relevance: %.2f)\n", r.result.Document.Content, r.combined))
		}
	}

	return sb.String()
}

// maxContentLen caps individual document content length to limit blast radius.
const maxContentLen = 2000

// sanitizeContent strips potentially dangerous content from documents retrieved
// from ChromaDB before injecting them into the LLM prompt.
func sanitizeContent(s string) string {
	if len(s) > maxContentLen {
		s = s[:maxContentLen]
	}
	// Replace markdown heading markers that could break prompt structure.
	s = strings.ReplaceAll(s, "\n#", "\n ")
	// Collapse multiple newlines to prevent layout injection.
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}
