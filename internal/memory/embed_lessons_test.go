package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// mockEmbedder implements Embedder for testing with fixed embeddings.
type mockEmbedder struct {
	embedding []float64
}

func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	result := make([][]float64, len(texts))
	for i := range texts {
		result[i] = m.embedding
	}
	return result, nil
}

func (m *mockEmbedder) EmbedOne(_ context.Context, _ string) ([]float64, error) {
	return m.embedding, nil
}

func TestEmbedLessons_DedupIncrementsTimesConfirmed(t *testing.T) {
	// Track what the server receives
	var mu sync.Mutex
	var updatedMetadatas []map[string]interface{}

	// Create a mock ChromaDB server with one existing lesson document
	existingEmbedding := []float64{0.1, 0.2, 0.3}
	colUUID := "col-uuid-lessons"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Collection lookup
		if r.Method == http.MethodGet && searchString(r.URL.Path, "/collections/ralph_lessons") && !searchString(r.URL.Path, colUUID) {
			json.NewEncoder(w).Encode(map[string]string{"id": colUUID})
			return
		}

		// Query — return a near-duplicate (distance < 0.1)
		if r.Method == http.MethodPost && searchString(r.URL.Path, "/collections/"+colUUID+"/query") {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ids":        [][]string{{"existing-doc-1"}},
				"documents":  [][]string{{"old pattern\nold recommendation"}},
				"metadatas":  [][]map[string]interface{}{{{"category": "testing", "confidence": 0.5, "times_confirmed": float64(1), "relevance_score": 0.5}}},
				"distances":  [][]float64{{0.05}}, // < 0.1 = near duplicate
				"embeddings": [][][]float64{{existingEmbedding}},
			})
			return
		}

		// Update — capture the metadata
		if r.Method == http.MethodPost && searchString(r.URL.Path, "/collections/"+colUUID+"/update") {
			var body struct {
				IDs       []string                   `json:"ids"`
				Metadatas []map[string]interface{}    `json:"metadatas"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			updatedMetadatas = append(updatedMetadatas, body.Metadatas...)
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]interface{}{})
			return
		}

		// Count — for cap enforcement
		if r.Method == http.MethodGet && searchString(r.URL.Path, "/count") {
			json.NewEncoder(w).Encode(1)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	embedder := &mockEmbedder{embedding: existingEmbedding}

	tmpDir := t.TempDir()

	lessons := []Lesson{
		{
			ID:             "lesson-1",
			Category:       "testing",
			Pattern:        "Always run tests before commit",
			Recommendation: "Use pre-commit hooks",
			Confidence:     0.5,
			TimesConfirmed: 1,
		},
	}

	err := EmbedLessons(context.Background(), client, embedder, lessons, tmpDir)
	if err != nil {
		t.Fatalf("EmbedLessons failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(updatedMetadatas) == 0 {
		t.Fatal("expected update call for near-duplicate, got none")
	}

	meta := updatedMetadatas[0]
	tc, ok := meta["times_confirmed"].(float64)
	if !ok {
		t.Fatalf("times_confirmed not found or not float64: %v", meta)
	}
	if tc != 2.0 {
		t.Errorf("expected times_confirmed=2, got %v", tc)
	}

	conf, ok := meta["confidence"].(float64)
	if !ok {
		t.Fatalf("confidence not found or not float64: %v", meta)
	}
	if conf != 0.6 {
		t.Errorf("expected confidence=0.6 (0.5+0.1), got %v", conf)
	}

	// Verify lessons.json was saved
	lf, err := LoadLessons(tmpDir)
	if err != nil {
		t.Fatalf("LoadLessons failed: %v", err)
	}
	if len(lf.Lessons) != 1 {
		t.Errorf("expected 1 lesson in file, got %d", len(lf.Lessons))
	}
}

func TestEmbedLessons_NewInsertWhenNoDuplicate(t *testing.T) {
	var mu sync.Mutex
	var addedCount int
	colUUID := "col-uuid-lessons"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodGet && searchString(r.URL.Path, "/collections/ralph_lessons") && !searchString(r.URL.Path, colUUID) {
			json.NewEncoder(w).Encode(map[string]string{"id": colUUID})
			return
		}

		// Query — return no results (empty collection)
		if r.Method == http.MethodPost && searchString(r.URL.Path, "/collections/"+colUUID+"/query") {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ids":       [][]string{},
				"documents": [][]string{},
				"metadatas": [][]map[string]interface{}{},
				"distances": [][]float64{},
			})
			return
		}

		// Add
		if r.Method == http.MethodPost && searchString(r.URL.Path, "/collections/"+colUUID+"/add") {
			mu.Lock()
			addedCount++
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]interface{}{})
			return
		}

		// Count
		if r.Method == http.MethodGet && searchString(r.URL.Path, "/count") {
			json.NewEncoder(w).Encode(1)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	embedder := &mockEmbedder{embedding: []float64{0.1, 0.2, 0.3}}

	tmpDir := t.TempDir()

	lessons := []Lesson{
		{
			ID:             "lesson-new",
			Category:       "architecture",
			Pattern:        "Keep modules small",
			Recommendation: "Split large packages",
			Confidence:     0.75,
			TimesConfirmed: 1,
		},
	}

	err := EmbedLessons(context.Background(), client, embedder, lessons, tmpDir)
	if err != nil {
		t.Fatalf("EmbedLessons failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if addedCount != 1 {
		t.Errorf("expected 1 add call, got %d", addedCount)
	}

	// Verify file persistence
	data, err := os.ReadFile(filepath.Join(tmpDir, ".ralph", "lessons.json"))
	if err != nil {
		t.Fatalf("lessons.json not created: %v", err)
	}
	if len(data) == 0 {
		t.Error("lessons.json is empty")
	}
}

func TestEmbedLessons_ConfidenceCappedAt1(t *testing.T) {
	var mu sync.Mutex
	var updatedMetadatas []map[string]interface{}
	colUUID := "col-uuid-lessons"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodGet && searchString(r.URL.Path, "/collections/ralph_lessons") && !searchString(r.URL.Path, colUUID) {
			json.NewEncoder(w).Encode(map[string]string{"id": colUUID})
			return
		}

		if r.Method == http.MethodPost && searchString(r.URL.Path, "/collections/"+colUUID+"/query") {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ids":        [][]string{{"existing-doc"}},
				"documents":  [][]string{{"pattern\nrec"}},
				"metadatas":  [][]map[string]interface{}{{{"confidence": 0.95, "times_confirmed": float64(5), "relevance_score": 0.95}}},
				"distances":  [][]float64{{0.02}},
				"embeddings": [][][]float64{{{0.1, 0.2, 0.3}}},
			})
			return
		}

		if r.Method == http.MethodPost && searchString(r.URL.Path, "/collections/"+colUUID+"/update") {
			var body struct {
				Metadatas []map[string]interface{} `json:"metadatas"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			updatedMetadatas = append(updatedMetadatas, body.Metadatas...)
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]interface{}{})
			return
		}

		if r.Method == http.MethodGet && searchString(r.URL.Path, "/count") {
			json.NewEncoder(w).Encode(1)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	embedder := &mockEmbedder{embedding: []float64{0.1, 0.2, 0.3}}

	lessons := []Lesson{
		{
			ID:         "lesson-cap",
			Pattern:    "test",
			Recommendation: "test",
			Confidence: 0.95,
			TimesConfirmed: 5,
		},
	}

	err := EmbedLessons(context.Background(), client, embedder, lessons, t.TempDir())
	if err != nil {
		t.Fatalf("EmbedLessons failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(updatedMetadatas) == 0 {
		t.Fatal("expected update call")
	}

	conf := updatedMetadatas[0]["confidence"].(float64)
	if conf != 1.0 {
		t.Errorf("expected confidence capped at 1.0, got %v", conf)
	}
}

func TestEmbedLessons_EmptyLessons(t *testing.T) {
	err := EmbedLessons(context.Background(), nil, nil, nil, "")
	if err != nil {
		t.Errorf("expected nil error for empty lessons, got %v", err)
	}
}
