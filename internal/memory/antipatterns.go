package memory

import (
	"context"
	"fmt"
	"strings"
)

// AntiPattern represents a recurring failure pattern detected from error and
// completion aggregation in ChromaDB.
type AntiPattern struct {
	Category        string   // fragile_area, flaky_test, common_oversight, high_friction
	Description     string
	FilesAffected   []string
	OccurrenceCount int
	AffectedStories []string
}

// antiPatternThreshold is the minimum number of occurrences required to flag
// an anti-pattern.
const antiPatternThreshold = 3

// DetectAntiPatterns queries ralph_errors and ralph_completions to detect
// recurring failure patterns. It returns anti-patterns grouped by category.
func DetectAntiPatterns(ctx context.Context, client *ChromaClient) ([]AntiPattern, error) {
	if client == nil {
		return nil, fmt.Errorf("chromadb client is nil")
	}

	var patterns []AntiPattern

	// Detect fragile areas: files appearing in 3+ error documents.
	fragile, err := detectFragileAreas(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("detect fragile areas: %w", err)
	}
	patterns = append(patterns, fragile...)

	// Detect high friction: files in stories with iteration_count > 3.
	friction, err := detectHighFriction(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("detect high friction: %w", err)
	}
	patterns = append(patterns, friction...)

	// Detect flaky tests and common oversights from repeated error types.
	repeated, err := detectRepeatedErrorTypes(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("detect repeated error types: %w", err)
	}
	patterns = append(patterns, repeated...)

	return patterns, nil
}

// detectFragileAreas finds files mentioned in 3+ error documents.
func detectFragileAreas(ctx context.Context, client *ChromaClient) ([]AntiPattern, error) {
	docs, err := client.GetAllDocuments(ctx, CollectionErrors.Name)
	if err != nil {
		return nil, err
	}

	// Count file occurrences across error documents and track affected stories.
	fileCounts := make(map[string]int)
	fileStories := make(map[string]map[string]bool)

	for _, doc := range docs {
		files := extractFiles(doc)
		storyID := doc.StoryID()
		for _, f := range files {
			fileCounts[f]++
			if storyID != "" {
				if fileStories[f] == nil {
					fileStories[f] = make(map[string]bool)
				}
				fileStories[f][storyID] = true
			}
		}
	}

	var patterns []AntiPattern
	for file, count := range fileCounts {
		if count >= antiPatternThreshold {
			stories := mapKeys(fileStories[file])
			patterns = append(patterns, AntiPattern{
				Category:        "fragile_area",
				Description:     fmt.Sprintf("File %s appears in %d error documents", file, count),
				FilesAffected:   []string{file},
				OccurrenceCount: count,
				AffectedStories: stories,
			})
		}
	}

	return patterns, nil
}

// detectHighFriction finds files associated with stories that have iteration_count > 3.
func detectHighFriction(ctx context.Context, client *ChromaClient) ([]AntiPattern, error) {
	docs, err := client.GetAllDocuments(ctx, CollectionCompletions.Name)
	if err != nil {
		return nil, err
	}

	// Track files from high-iteration stories.
	fileCounts := make(map[string]int)
	fileStories := make(map[string]map[string]bool)

	for _, doc := range docs {
		iterCount := metadataFloat(doc.Metadata, "iteration_count")
		if iterCount <= 3 {
			continue
		}

		files := extractFiles(doc)
		storyID := doc.StoryID()
		for _, f := range files {
			fileCounts[f]++
			if storyID != "" {
				if fileStories[f] == nil {
					fileStories[f] = make(map[string]bool)
				}
				fileStories[f][storyID] = true
			}
		}
	}

	var patterns []AntiPattern
	for file, count := range fileCounts {
		if count >= antiPatternThreshold {
			stories := mapKeys(fileStories[file])
			patterns = append(patterns, AntiPattern{
				Category:        "high_friction",
				Description:     fmt.Sprintf("File %s involved in %d high-iteration stories", file, count),
				FilesAffected:   []string{file},
				OccurrenceCount: count,
				AffectedStories: stories,
			})
		}
	}

	return patterns, nil
}

// detectRepeatedErrorTypes scans error documents for repeated error_type values
// and classifies them as flaky_test or common_oversight.
func detectRepeatedErrorTypes(ctx context.Context, client *ChromaClient) ([]AntiPattern, error) {
	docs, err := client.GetAllDocuments(ctx, CollectionErrors.Name)
	if err != nil {
		return nil, err
	}

	type errorInfo struct {
		count   int
		files   map[string]bool
		stories map[string]bool
	}

	errorTypes := make(map[string]*errorInfo)

	for _, doc := range docs {
		errType := metadataString(doc.Metadata, "error_type")
		if errType == "" {
			continue
		}

		info, ok := errorTypes[errType]
		if !ok {
			info = &errorInfo{
				files:   make(map[string]bool),
				stories: make(map[string]bool),
			}
			errorTypes[errType] = info
		}

		info.count++
		for _, f := range extractFiles(doc) {
			info.files[f] = true
		}
		if sid := doc.StoryID(); sid != "" {
			info.stories[sid] = true
		}
	}

	var patterns []AntiPattern
	for errType, info := range errorTypes {
		if info.count < antiPatternThreshold {
			continue
		}

		category := "common_oversight"
		if strings.Contains(strings.ToLower(errType), "test") || strings.Contains(strings.ToLower(errType), "flaky") {
			category = "flaky_test"
		}

		patterns = append(patterns, AntiPattern{
			Category:        category,
			Description:     fmt.Sprintf("Error type %q occurred %d times", errType, info.count),
			FilesAffected:   mapKeys(info.files),
			OccurrenceCount: info.count,
			AffectedStories: mapKeys(info.stories),
		})
	}

	return patterns, nil
}

// extractFiles returns file paths from a document's metadata "files" field
// (comma-separated string) or "file" field (single string).
func extractFiles(doc Document) []string {
	if doc.Metadata == nil {
		return nil
	}

	var files []string

	if filesStr, ok := doc.Metadata["files"].(string); ok && filesStr != "" {
		for _, f := range strings.Split(filesStr, ",") {
			f = strings.TrimSpace(f)
			if f != "" {
				files = append(files, f)
			}
		}
	}

	if file, ok := doc.Metadata["file"].(string); ok && file != "" {
		files = append(files, file)
	}

	return files
}

// metadataFloat extracts a float64 value from metadata.
func metadataFloat(m map[string]interface{}, key string) float64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	f, ok := v.(float64)
	if !ok {
		return 0
	}
	return f
}

// metadataString extracts a string value from metadata.
func metadataString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

// mapKeys returns the keys of a map as a sorted slice.
func mapKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
