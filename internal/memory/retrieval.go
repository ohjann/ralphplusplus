package memory

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// RetrievalOptions controls how semantic retrieval behaves.
type RetrievalOptions struct {
	TopK      int     // Number of results per collection (default 5)
	MinScore  float64 // Minimum similarity score threshold (default 0.7)
	MaxTokens int     // Token budget for formatted output (default 2000)
	Disabled  bool    // If true, return empty string immediately
}

// DefaultRetrievalOptions returns options with sensible defaults.
func DefaultRetrievalOptions() RetrievalOptions {
	return RetrievalOptions{
		TopK:      5,
		MinScore:  0.7,
		MaxTokens: 2000,
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
}

// RetrieveContext queries all memory collections for content relevant to a story,
// ranks results by relevance and recency, applies a token budget, and formats
// the output as markdown for prompt injection.
func RetrieveContext(
	ctx context.Context,
	client *ChromaClient,
	embedder Embedder,
	storyTitle string,
	storyDescription string,
	acceptanceCriteria []string,
	opts RetrievalOptions,
) (string, error) {
	if opts.Disabled {
		return "", nil
	}

	// Apply defaults for zero values.
	if opts.TopK <= 0 {
		opts.TopK = 5
	}
	if opts.MinScore <= 0 {
		opts.MinScore = 0.7
	}
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 2000
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
		return "", fmt.Errorf("embed query: %w", err)
	}

	// Query all collections and gather results.
	var ranked []rankedResult
	now := time.Now()

	for _, col := range AllCollections() {
		results, err := client.QueryCollection(ctx, col.Name, embedding, opts.TopK)
		if err != nil {
			// Skip collections that don't exist or fail — partial results are fine.
			continue
		}

		for _, r := range results {
			if r.Score < opts.MinScore {
				continue
			}

			recencyWeight := computeRecencyWeight(r.Document.LastConfirmed(), now)
			ranked = append(ranked, rankedResult{
				result:     r,
				collection: col.Name,
				combined:   r.Score * recencyWeight,
			})
		}
	}

	if len(ranked) == 0 {
		return "", nil
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

	return formatMarkdown(selected), nil
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
	// Exponential decay: half-life of 30 days.
	return math.Exp(-0.693 * days / 30)
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
