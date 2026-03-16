package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"github.com/eoghanhynes/ralph/internal/debuglog"
	"net/http"
	"os"
	"sync"
	"time"
)

// Embedder generates vector embeddings for text.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
	EmbedOne(ctx context.Context, text string) ([]float64, error)
}

// AnthropicEmbedder calls the Voyage AI embedding API (Anthropic's recommended
// embedding provider) to generate vector embeddings.
type AnthropicEmbedder struct {
	apiKey     string
	endpoint   string
	model      string
	maxBatch   int
	maxRetries int
	httpClient *http.Client

	logOnce sync.Once
}

// voyageRequest is the request body for the Voyage AI embeddings API.
type voyageRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// voyageResponse is the response from the Voyage AI embeddings API.
type voyageResponse struct {
	Data []voyageEmbedding `json:"data"`
}

// voyageEmbedding is a single embedding in the API response.
type voyageEmbedding struct {
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}

// voyageErrorResponse is returned by the API on errors.
type voyageErrorResponse struct {
	Detail string `json:"detail"`
}

// NewAnthropicEmbedder creates a new AnthropicEmbedder. It reads the API key
// from VOYAGE_API_KEY, falling back to ANTHROPIC_API_KEY if not set.
func NewAnthropicEmbedder() (*AnthropicEmbedder, error) {
	apiKey := os.Getenv("VOYAGE_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("embedding API key not configured: set VOYAGE_API_KEY or ANTHROPIC_API_KEY")
	}

	return &AnthropicEmbedder{
		apiKey:     apiKey,
		endpoint:   "https://api.voyageai.com/v1/embeddings",
		model:      "voyage-3",
		maxBatch:   128,
		maxRetries: 3,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// Embed generates embeddings for the given texts. If texts exceed the batch
// size limit (128), they are split into multiple API calls.
func (e *AnthropicEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	var allEmbeddings [][]float64

	for i := 0; i < len(texts); i += e.maxBatch {
		end := i + e.maxBatch
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[i:end]

		embeddings, err := e.callAPI(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("embedding batch %d-%d: %w", i, end-1, err)
		}

		allEmbeddings = append(allEmbeddings, embeddings...)
	}

	return allEmbeddings, nil
}

// EmbedOne is a convenience method that generates an embedding for a single text.
func (e *AnthropicEmbedder) EmbedOne(ctx context.Context, text string) ([]float64, error) {
	results, err := e.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no embedding returned for text")
	}
	return results[0], nil
}

// callAPI makes a single API call to the Voyage AI embeddings endpoint with
// retry logic for rate limiting (429 responses).
func (e *AnthropicEmbedder) callAPI(ctx context.Context, texts []string) ([][]float64, error) {
	reqBody := voyageRequest{
		Model: e.model,
		Input: texts,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	backoff := time.Second
	var lastErr error

	for attempt := 0; attempt <= e.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				backoff *= 2
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+e.apiKey)

		resp, err := e.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("http request: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		switch {
		case resp.StatusCode == http.StatusOK:
			var result voyageResponse
			if err := json.Unmarshal(respBody, &result); err != nil {
				return nil, fmt.Errorf("unmarshal response: %w", err)
			}
			embeddings := make([][]float64, len(result.Data))
			for _, d := range result.Data {
				embeddings[d.Index] = d.Embedding
			}

			// Log dimensions on first successful call.
			if len(embeddings) > 0 && len(embeddings[0]) > 0 {
				e.logOnce.Do(func() {
					debuglog.Log("voyage embedding dimensions: %d", len(embeddings[0]))
				})
			}

			return embeddings, nil

		case resp.StatusCode == http.StatusTooManyRequests:
			lastErr = fmt.Errorf("rate limited (429): %s", parseErrorDetail(respBody))
			continue

		case resp.StatusCode == http.StatusUnauthorized:
			return nil, fmt.Errorf("authentication failed (401): check your VOYAGE_API_KEY or ANTHROPIC_API_KEY — %s", parseErrorDetail(respBody))

		case resp.StatusCode == http.StatusBadRequest:
			return nil, fmt.Errorf("invalid request (400): %s", parseErrorDetail(respBody))

		case resp.StatusCode >= 500:
			return nil, fmt.Errorf("server error (%d): %s", resp.StatusCode, parseErrorDetail(respBody))

		default:
			return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, parseErrorDetail(respBody))
		}
	}

	return nil, fmt.Errorf("max retries exceeded for rate limiting: %w", lastErr)
}

// parseErrorDetail extracts a detail message from an error response body.
func parseErrorDetail(body []byte) string {
	var errResp voyageErrorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Detail != "" {
		return errResp.Detail
	}
	return string(body)
}
