package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestEmbedder creates an AnthropicEmbedder pointed at the given test server.
func newTestEmbedder(serverURL string) *AnthropicEmbedder {
	return &AnthropicEmbedder{
		apiKey:     "test-key",
		endpoint:   serverURL + "/v1/embeddings",
		model:      "voyage-3",
		maxBatch:   128,
		maxRetries: 3,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// fakeEmbedding returns a deterministic embedding for testing.
func fakeEmbedding(dim int) []float64 {
	e := make([]float64, dim)
	for i := range e {
		e[i] = float64(i) * 0.01
	}
	return e
}

func TestEmbedOne_RequestBody(t *testing.T) {
	var gotReq voyageRequest
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		resp := voyageResponse{
			Data: []voyageEmbedding{
				{Embedding: fakeEmbedding(3), Index: 0},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	emb := newTestEmbedder(srv.URL)
	result, err := emb.EmbedOne(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("EmbedOne: %v", err)
	}

	// Verify request fields.
	if gotReq.Model != "voyage-3" {
		t.Errorf("model = %q, want %q", gotReq.Model, "voyage-3")
	}
	if len(gotReq.Input) != 1 || gotReq.Input[0] != "hello world" {
		t.Errorf("input = %v, want [\"hello world\"]", gotReq.Input)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("auth = %q, want %q", gotAuth, "Bearer test-key")
	}
	if len(result) != 3 {
		t.Errorf("embedding length = %d, want 3", len(result))
	}
}

func TestEmbed_BatchSplitting(t *testing.T) {
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		var req voyageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}

		// Verify each batch is at most 128 items.
		if len(req.Input) > 128 {
			t.Errorf("batch size = %d, exceeds max 128", len(req.Input))
		}

		data := make([]voyageEmbedding, len(req.Input))
		for i := range req.Input {
			data[i] = voyageEmbedding{Embedding: fakeEmbedding(3), Index: i}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(voyageResponse{Data: data})
	}))
	defer srv.Close()

	emb := newTestEmbedder(srv.URL)

	// Send 300 texts — should result in 3 batches (128 + 128 + 44).
	texts := make([]string, 300)
	for i := range texts {
		texts[i] = "text"
	}

	results, err := emb.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if got := callCount.Load(); got != 3 {
		t.Errorf("API calls = %d, want 3", got)
	}
	if len(results) != 300 {
		t.Errorf("results = %d, want 300", len(results))
	}
}

func TestEmbed_RateLimitRetry(t *testing.T) {
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			// First call: 429 rate limit.
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(voyageErrorResponse{Detail: "rate limited"})
			return
		}
		// Second call: success.
		var req voyageRequest
		json.NewDecoder(r.Body).Decode(&req)
		data := make([]voyageEmbedding, len(req.Input))
		for i := range req.Input {
			data[i] = voyageEmbedding{Embedding: fakeEmbedding(3), Index: i}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(voyageResponse{Data: data})
	}))
	defer srv.Close()

	emb := newTestEmbedder(srv.URL)
	// Use a short backoff to keep test fast. We override maxRetries to ensure retry happens.
	emb.maxRetries = 2

	result, err := emb.EmbedOne(context.Background(), "retry me")
	if err != nil {
		t.Fatalf("EmbedOne after retry: %v", err)
	}
	if callCount.Load() != 2 {
		t.Errorf("API calls = %d, want 2 (1 retry)", callCount.Load())
	}
	if len(result) != 3 {
		t.Errorf("embedding length = %d, want 3", len(result))
	}
}

func TestEmbed_AuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(voyageErrorResponse{Detail: "invalid api key"})
	}))
	defer srv.Close()

	emb := newTestEmbedder(srv.URL)
	_, err := emb.EmbedOne(context.Background(), "fail auth")
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "API_KEY") {
		t.Errorf("error should mention API key, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "401") {
		t.Errorf("error should mention 401, got: %s", errMsg)
	}
}

func TestNewAnthropicEmbedder_APIKeyPrecedence(t *testing.T) {
	// Save and restore env.
	origVoyage := getenvSafe("VOYAGE_API_KEY")
	origAnthropic := getenvSafe("ANTHROPIC_API_KEY")
	defer func() {
		setenvSafe(t, "VOYAGE_API_KEY", origVoyage)
		setenvSafe(t, "ANTHROPIC_API_KEY", origAnthropic)
	}()

	// Test: VOYAGE_API_KEY takes precedence.
	t.Setenv("VOYAGE_API_KEY", "voyage-key-123")
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-key-456")

	emb, err := NewAnthropicEmbedder()
	if err != nil {
		t.Fatalf("NewAnthropicEmbedder: %v", err)
	}
	if emb.apiKey != "voyage-key-123" {
		t.Errorf("apiKey = %q, want %q (VOYAGE_API_KEY should take precedence)", emb.apiKey, "voyage-key-123")
	}

	// Test: Falls back to ANTHROPIC_API_KEY.
	t.Setenv("VOYAGE_API_KEY", "")

	emb2, err := NewAnthropicEmbedder()
	if err != nil {
		t.Fatalf("NewAnthropicEmbedder: %v", err)
	}
	if emb2.apiKey != "anthropic-key-456" {
		t.Errorf("apiKey = %q, want %q (should fall back to ANTHROPIC_API_KEY)", emb2.apiKey, "anthropic-key-456")
	}

	// Test: No key set returns error.
	t.Setenv("ANTHROPIC_API_KEY", "")

	_, err = NewAnthropicEmbedder()
	if err == nil {
		t.Fatal("expected error when no API key set, got nil")
	}
}

func getenvSafe(key string) string {
	// Just a wrapper — t.Setenv handles restore automatically.
	return ""
}

func setenvSafe(t *testing.T, key, value string) {
	t.Helper()
	// t.Setenv already restores; this is a no-op for deferred restore pattern.
}
