package memory

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// --- Type serialization tests ---

func TestDocumentJSONRoundTrip(t *testing.T) {
	doc := Document{
		ID:        "doc-1",
		Content:   "some content",
		Embedding: []float64{0.1, 0.2, 0.3},
		Metadata: map[string]interface{}{
			"story_id":        "P1-001",
			"relevance_score": 0.95,
			"last_confirmed":  "2026-01-15T10:00:00Z",
		},
	}

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Document
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != doc.ID {
		t.Errorf("ID = %q, want %q", got.ID, doc.ID)
	}
	if got.Content != doc.Content {
		t.Errorf("Content = %q, want %q", got.Content, doc.Content)
	}
	if len(got.Embedding) != len(doc.Embedding) {
		t.Fatalf("Embedding len = %d, want %d", len(got.Embedding), len(doc.Embedding))
	}
	for i, v := range got.Embedding {
		if v != doc.Embedding[i] {
			t.Errorf("Embedding[%d] = %f, want %f", i, v, doc.Embedding[i])
		}
	}
	if got.StoryID() != "P1-001" {
		t.Errorf("StoryID() = %q, want %q", got.StoryID(), "P1-001")
	}
	if got.RelevanceScore() != 0.95 {
		t.Errorf("RelevanceScore() = %f, want 0.95", got.RelevanceScore())
	}
	wantTime, _ := time.Parse(time.RFC3339, "2026-01-15T10:00:00Z")
	if !got.LastConfirmed().Equal(wantTime) {
		t.Errorf("LastConfirmed() = %v, want %v", got.LastConfirmed(), wantTime)
	}
}

func TestDocumentMetadataHelpers_NilMetadata(t *testing.T) {
	doc := Document{}
	if doc.RelevanceScore() != 0 {
		t.Errorf("RelevanceScore() = %f, want 0", doc.RelevanceScore())
	}
	if doc.StoryID() != "" {
		t.Errorf("StoryID() = %q, want empty", doc.StoryID())
	}
	if !doc.LastConfirmed().IsZero() {
		t.Errorf("LastConfirmed() should be zero time")
	}
}

func TestQueryResultJSONRoundTrip(t *testing.T) {
	qr := QueryResult{
		Document: Document{
			ID:      "qr-1",
			Content: "query result content",
		},
		Score:    0.85,
		Distance: 0.15,
	}

	data, err := json.Marshal(qr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got QueryResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Document.ID != qr.Document.ID {
		t.Errorf("Document.ID = %q, want %q", got.Document.ID, qr.Document.ID)
	}
	if got.Score != qr.Score {
		t.Errorf("Score = %f, want %f", got.Score, qr.Score)
	}
	if got.Distance != qr.Distance {
		t.Errorf("Distance = %f, want %f", got.Distance, qr.Distance)
	}
}

// --- Client constructor test ---

func TestNewClient(t *testing.T) {
	client := NewClient("http://localhost:8000")
	if client.baseURL != "http://localhost:8000" {
		t.Errorf("baseURL = %q, want %q", client.baseURL, "http://localhost:8000")
	}
	if client.http == nil {
		t.Error("http client should not be nil")
	}
}

// --- Mock server helpers ---

// collectionServer returns an httptest.Server that handles:
// - GET /api/v1/collections/{name} -> returns collection with given ID
// - POST /api/v1/collections/{id}/add -> records request body, returns 200
// - POST /api/v1/collections/{id}/query -> returns mock query results
// - GET /api/v1/collections/{id}/count -> returns mock count
func collectionServer(t *testing.T, collectionID string, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle collection lookup by name
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/collections/test-collection" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"id": collectionID})
			return
		}
		handler(w, r)
	}))
}

// --- AddDocuments tests ---

func TestAddDocuments_ConstructsCorrectRequest(t *testing.T) {
	var capturedBody map[string]interface{}
	var capturedPath string
	var capturedMethod string

	srv := collectionServer(t, "col-uuid-123", func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedMethod = r.Method

		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedBody)

		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	client := NewClient(srv.URL)
	docs := []Document{
		{
			ID:        "doc-1",
			Content:   "hello world",
			Embedding: []float64{0.1, 0.2},
			Metadata:  map[string]interface{}{"key": "val"},
		},
		{
			ID:        "doc-2",
			Content:   "second doc",
			Embedding: []float64{0.3, 0.4},
		},
	}

	err := client.AddDocuments(context.Background(), "test-collection", docs)
	if err != nil {
		t.Fatalf("AddDocuments: %v", err)
	}

	if capturedMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", capturedMethod)
	}
	if capturedPath != "/api/v1/collections/col-uuid-123/add" {
		t.Errorf("path = %q, want /api/v1/collections/col-uuid-123/add", capturedPath)
	}

	// Verify request body structure
	ids, ok := capturedBody["ids"].([]interface{})
	if !ok || len(ids) != 2 {
		t.Fatalf("ids length = %v, want 2", ids)
	}
	if ids[0] != "doc-1" || ids[1] != "doc-2" {
		t.Errorf("ids = %v, want [doc-1, doc-2]", ids)
	}

	documents, ok := capturedBody["documents"].([]interface{})
	if !ok || len(documents) != 2 {
		t.Fatalf("documents length = %v, want 2", documents)
	}

	embeddings, ok := capturedBody["embeddings"].([]interface{})
	if !ok || len(embeddings) != 2 {
		t.Fatalf("embeddings length = %v, want 2", embeddings)
	}

	metadatas, ok := capturedBody["metadatas"].([]interface{})
	if !ok || len(metadatas) != 2 {
		t.Fatalf("metadatas length = %v, want 2", metadatas)
	}
}

func TestAddDocuments_EmptySlice(t *testing.T) {
	client := NewClient("http://localhost:9999") // no server needed
	err := client.AddDocuments(context.Background(), "any", nil)
	if err != nil {
		t.Errorf("AddDocuments with empty docs should return nil, got: %v", err)
	}
}

// --- QueryCollection tests ---

func TestQueryCollection_ConstructsRequestAndParsesResponse(t *testing.T) {
	var capturedBody map[string]interface{}

	srv := collectionServer(t, "col-uuid-456", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ids":        [][]string{{"r1", "r2"}},
			"documents":  [][]string{{"content 1", "content 2"}},
			"metadatas":  [][]map[string]interface{}{{{"key": "v1"}, {"key": "v2"}}},
			"distances":  [][]float64{{0.1, 0.3}},
			"embeddings": [][][]float64{{{0.5, 0.6}, {0.7, 0.8}}},
		})
	})
	defer srv.Close()

	client := NewClient(srv.URL)
	results, err := client.QueryCollection(context.Background(), "test-collection", []float64{0.1, 0.2, 0.3}, 5)
	if err != nil {
		t.Fatalf("QueryCollection: %v", err)
	}

	// Verify request body
	qe, ok := capturedBody["query_embeddings"].([]interface{})
	if !ok || len(qe) != 1 {
		t.Fatalf("query_embeddings should have 1 entry, got %v", qe)
	}
	nResults, ok := capturedBody["n_results"].(float64)
	if !ok || int(nResults) != 5 {
		t.Errorf("n_results = %v, want 5", nResults)
	}

	// Verify parsed results
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}

	if results[0].Document.ID != "r1" {
		t.Errorf("results[0].ID = %q, want r1", results[0].Document.ID)
	}
	if results[0].Document.Content != "content 1" {
		t.Errorf("results[0].Content = %q, want 'content 1'", results[0].Document.Content)
	}
	if results[0].Distance != 0.1 {
		t.Errorf("results[0].Distance = %f, want 0.1", results[0].Distance)
	}
	if results[0].Score != 0.9 {
		t.Errorf("results[0].Score = %f, want 0.9", results[0].Score)
	}
	if len(results[0].Document.Embedding) != 2 {
		t.Errorf("results[0].Embedding len = %d, want 2", len(results[0].Document.Embedding))
	}

	if results[1].Document.ID != "r2" {
		t.Errorf("results[1].ID = %q, want r2", results[1].Document.ID)
	}
}

func TestQueryCollection_EmptyResults(t *testing.T) {
	srv := collectionServer(t, "col-uuid-empty", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ids":        [][]string{{}},
			"documents":  [][]string{{}},
			"metadatas":  [][]map[string]interface{}{{}},
			"distances":  [][]float64{{}},
			"embeddings": [][][]float64{{}},
		})
	})
	defer srv.Close()

	client := NewClient(srv.URL)
	results, err := client.QueryCollection(context.Background(), "test-collection", []float64{0.1}, 3)
	if err != nil {
		t.Fatalf("QueryCollection: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty query, got %v", results)
	}
}

// --- CountDocuments tests ---

func TestCountDocuments_ParsesResponse(t *testing.T) {
	srv := collectionServer(t, "col-uuid-cnt", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/collections/col-uuid-cnt/count" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("42"))
	})
	defer srv.Close()

	client := NewClient(srv.URL)
	count, err := client.CountDocuments(context.Background(), "test-collection")
	if err != nil {
		t.Fatalf("CountDocuments: %v", err)
	}
	if count != 42 {
		t.Errorf("count = %d, want 42", count)
	}
}

// --- Error handling tests ---

func TestClientMethods_Non2xxErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Collection lookup succeeds, but operations fail
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/collections/test-collection" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"id": "col-err"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	ctx := context.Background()

	// AddDocuments
	err := client.AddDocuments(ctx, "test-collection", []Document{{ID: "x", Content: "y", Embedding: []float64{0.1}}})
	if err == nil {
		t.Error("AddDocuments: expected error for 500 response")
	}

	// QueryCollection
	_, err = client.QueryCollection(ctx, "test-collection", []float64{0.1}, 1)
	if err == nil {
		t.Error("QueryCollection: expected error for 500 response")
	}

	// CountDocuments
	_, err = client.CountDocuments(ctx, "test-collection")
	if err == nil {
		t.Error("CountDocuments: expected error for 500 response")
	}
}

func TestClientMethods_CollectionLookupError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("collection not found"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	ctx := context.Background()

	err := client.AddDocuments(ctx, "missing", []Document{{ID: "x", Content: "y", Embedding: []float64{0.1}}})
	if err == nil {
		t.Error("expected error when collection not found")
	}

	_, err = client.QueryCollection(ctx, "missing", []float64{0.1}, 1)
	if err == nil {
		t.Error("expected error when collection not found")
	}

	_, err = client.CountDocuments(ctx, "missing")
	if err == nil {
		t.Error("expected error when collection not found")
	}
}

func TestCreateCollection_Non2xxError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	err := client.CreateCollection(context.Background(), "bad-col")
	if err == nil {
		t.Error("expected error for 400 response")
	}
}

// --- Connection failure tests ---

func TestClientMethods_ConnectionFailure(t *testing.T) {
	// Use a closed server to simulate connection failure
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	client := NewClient(srv.URL)
	ctx := context.Background()

	// CreateCollection
	err := client.CreateCollection(ctx, "test")
	if err == nil {
		t.Error("CreateCollection: expected error for connection failure")
	}

	// AddDocuments - will fail on collection lookup
	err = client.AddDocuments(ctx, "test", []Document{{ID: "x", Content: "y", Embedding: []float64{0.1}}})
	if err == nil {
		t.Error("AddDocuments: expected error for connection failure")
	}

	// QueryCollection
	_, err = client.QueryCollection(ctx, "test", []float64{0.1}, 1)
	if err == nil {
		t.Error("QueryCollection: expected error for connection failure")
	}

	// CountDocuments
	_, err = client.CountDocuments(ctx, "test")
	if err == nil {
		t.Error("CountDocuments: expected error for connection failure")
	}
}
