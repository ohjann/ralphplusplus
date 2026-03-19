package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockChromaServer creates a test HTTP server that serves mock ChromaDB responses.
// collections maps collection names to their documents.
func mockChromaServer(t *testing.T, collections map[string][]Document) *httptest.Server {
	t.Helper()

	// Assign stable UUIDs for each collection.
	collectionIDs := make(map[string]string)
	i := 0
	for name := range collections {
		collectionIDs[name] = fmt.Sprintf("col-uuid-%d", i)
		i++
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Handle collection lookup: GET .../collections/{name}
		for name, id := range collectionIDs {
			if r.Method == http.MethodGet && contains(r.URL.Path, "/collections/"+name) {
				json.NewEncoder(w).Encode(map[string]string{"id": id})
				return
			}
		}

		// Handle get all documents: POST .../collections/{uuid}/get
		for name, id := range collectionIDs {
			if r.Method == http.MethodPost && contains(r.URL.Path, "/collections/"+id+"/get") {
				docs := collections[name]
				ids := make([]string, len(docs))
				documents := make([]string, len(docs))
				metadatas := make([]map[string]interface{}, len(docs))
				for j, d := range docs {
					ids[j] = d.ID
					documents[j] = d.Content
					metadatas[j] = d.Metadata
					if metadatas[j] == nil {
						metadatas[j] = map[string]interface{}{}
					}
				}
				json.NewEncoder(w).Encode(map[string]interface{}{
					"ids":       ids,
					"documents": documents,
					"metadatas": metadatas,
				})
				return
			}
		}

		http.NotFound(w, r)
	}))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestDetectAntiPatterns_NilClient(t *testing.T) {
	_, err := DetectAntiPatterns(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestDetectAntiPatterns_Empty(t *testing.T) {
	srv := mockChromaServer(t, map[string][]Document{
		CollectionErrors.Name:      {},
		CollectionCompletions.Name: {},
	})
	defer srv.Close()

	client := NewClient(srv.URL)
	patterns, err := DetectAntiPatterns(context.Background(), client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(patterns) != 0 {
		t.Fatalf("expected 0 patterns, got %d", len(patterns))
	}
}

func TestDetectAntiPatterns_FragileArea(t *testing.T) {
	// Create 3 error documents all mentioning the same file.
	errorDocs := []Document{
		{ID: "e1", Content: "error 1", Metadata: map[string]interface{}{"files": "internal/runner/runner.go", "story_id": "S1"}},
		{ID: "e2", Content: "error 2", Metadata: map[string]interface{}{"files": "internal/runner/runner.go", "story_id": "S2"}},
		{ID: "e3", Content: "error 3", Metadata: map[string]interface{}{"files": "internal/runner/runner.go,internal/other.go", "story_id": "S3"}},
	}

	srv := mockChromaServer(t, map[string][]Document{
		CollectionErrors.Name:      errorDocs,
		CollectionCompletions.Name: {},
	})
	defer srv.Close()

	client := NewClient(srv.URL)
	patterns, err := DetectAntiPatterns(context.Background(), client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, p := range patterns {
		if p.Category == "fragile_area" && p.FilesAffected[0] == "internal/runner/runner.go" {
			found = true
			if p.OccurrenceCount != 3 {
				t.Errorf("expected occurrence count 3, got %d", p.OccurrenceCount)
			}
			if len(p.AffectedStories) != 3 {
				t.Errorf("expected 3 affected stories, got %d", len(p.AffectedStories))
			}
		}
	}
	if !found {
		t.Error("expected fragile_area pattern for internal/runner/runner.go")
	}
}

func TestDetectAntiPatterns_BelowThreshold(t *testing.T) {
	// Only 2 errors for a file — should NOT produce an anti-pattern.
	errorDocs := []Document{
		{ID: "e1", Content: "error 1", Metadata: map[string]interface{}{"files": "internal/foo.go", "story_id": "S1"}},
		{ID: "e2", Content: "error 2", Metadata: map[string]interface{}{"files": "internal/foo.go", "story_id": "S2"}},
	}

	srv := mockChromaServer(t, map[string][]Document{
		CollectionErrors.Name:      errorDocs,
		CollectionCompletions.Name: {},
	})
	defer srv.Close()

	client := NewClient(srv.URL)
	patterns, err := DetectAntiPatterns(context.Background(), client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(patterns) != 0 {
		t.Fatalf("expected 0 patterns (below threshold), got %d", len(patterns))
	}
}

func TestDetectAntiPatterns_HighFriction(t *testing.T) {
	// 3 completions with iteration_count > 3, all touching the same file.
	completionDocs := []Document{
		{ID: "c1", Content: "comp 1", Metadata: map[string]interface{}{"iteration_count": float64(5), "files": "internal/config/config.go", "story_id": "S1"}},
		{ID: "c2", Content: "comp 2", Metadata: map[string]interface{}{"iteration_count": float64(4), "files": "internal/config/config.go", "story_id": "S2"}},
		{ID: "c3", Content: "comp 3", Metadata: map[string]interface{}{"iteration_count": float64(6), "files": "internal/config/config.go", "story_id": "S3"}},
	}

	srv := mockChromaServer(t, map[string][]Document{
		CollectionErrors.Name:      {},
		CollectionCompletions.Name: completionDocs,
	})
	defer srv.Close()

	client := NewClient(srv.URL)
	patterns, err := DetectAntiPatterns(context.Background(), client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, p := range patterns {
		if p.Category == "high_friction" {
			found = true
			if p.OccurrenceCount != 3 {
				t.Errorf("expected occurrence count 3, got %d", p.OccurrenceCount)
			}
		}
	}
	if !found {
		t.Error("expected high_friction pattern")
	}
}

func TestDetectAntiPatterns_RepeatedErrorType(t *testing.T) {
	// 3 errors with the same error_type.
	errorDocs := []Document{
		{ID: "e1", Content: "err", Metadata: map[string]interface{}{"error_type": "nil_pointer", "file": "a.go", "story_id": "S1"}},
		{ID: "e2", Content: "err", Metadata: map[string]interface{}{"error_type": "nil_pointer", "file": "b.go", "story_id": "S2"}},
		{ID: "e3", Content: "err", Metadata: map[string]interface{}{"error_type": "nil_pointer", "file": "c.go", "story_id": "S3"}},
	}

	srv := mockChromaServer(t, map[string][]Document{
		CollectionErrors.Name:      errorDocs,
		CollectionCompletions.Name: {},
	})
	defer srv.Close()

	client := NewClient(srv.URL)
	patterns, err := DetectAntiPatterns(context.Background(), client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, p := range patterns {
		if p.Category == "common_oversight" && p.OccurrenceCount == 3 {
			found = true
			if len(p.FilesAffected) != 3 {
				t.Errorf("expected 3 files affected, got %d", len(p.FilesAffected))
			}
		}
	}
	if !found {
		t.Error("expected common_oversight pattern for nil_pointer")
	}
}

func TestDetectAntiPatterns_FlakyTest(t *testing.T) {
	// 3 errors with a test-related error_type.
	errorDocs := []Document{
		{ID: "e1", Content: "err", Metadata: map[string]interface{}{"error_type": "flaky_test_timeout", "story_id": "S1"}},
		{ID: "e2", Content: "err", Metadata: map[string]interface{}{"error_type": "flaky_test_timeout", "story_id": "S2"}},
		{ID: "e3", Content: "err", Metadata: map[string]interface{}{"error_type": "flaky_test_timeout", "story_id": "S3"}},
	}

	srv := mockChromaServer(t, map[string][]Document{
		CollectionErrors.Name:      errorDocs,
		CollectionCompletions.Name: {},
	})
	defer srv.Close()

	client := NewClient(srv.URL)
	patterns, err := DetectAntiPatterns(context.Background(), client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, p := range patterns {
		if p.Category == "flaky_test" {
			found = true
		}
	}
	if !found {
		t.Error("expected flaky_test pattern")
	}
}
