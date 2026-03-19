package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ChromaClient wraps an HTTP client for communicating with ChromaDB's REST API.
type ChromaClient struct {
	baseURL string
	http    *http.Client
}

// NewClient creates a new ChromaDB client pointed at the given base URL.
func NewClient(baseURL string) *ChromaClient {
	return &ChromaClient{
		baseURL: baseURL,
		http:    &http.Client{},
	}
}

// CreateCollection creates a collection in ChromaDB using get_or_create semantics.
// It is idempotent — calling it multiple times with the same name is safe.
func (c *ChromaClient) CreateCollection(ctx context.Context, name string) error {
	body := map[string]interface{}{
		"name":          name,
		"get_or_create": true,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal create collection request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v2/tenants/default_tenant/databases/default_database/collections", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create collection request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.readError(resp, "create collection %q", name)
	}
	return nil
}

// DeleteCollection deletes a collection by name from ChromaDB.
func (c *ChromaClient) DeleteCollection(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/api/v2/tenants/default_tenant/databases/default_database/collections/"+name, nil)
	if err != nil {
		return fmt.Errorf("delete collection request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("delete collection %q: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.readError(resp, "delete collection %q", name)
	}
	return nil
}

// AddDocuments adds documents with pre-computed embeddings to a collection.
func (c *ChromaClient) AddDocuments(ctx context.Context, collection string, docs []Document) error {
	if len(docs) == 0 {
		return nil
	}

	collectionID, err := c.getCollectionID(ctx, collection)
	if err != nil {
		return fmt.Errorf("add documents to %q: %w", collection, err)
	}

	ids := make([]string, len(docs))
	documents := make([]string, len(docs))
	embeddings := make([][]float64, len(docs))
	metadatas := make([]map[string]interface{}, len(docs))

	for i, doc := range docs {
		ids[i] = doc.ID
		documents[i] = doc.Content
		embeddings[i] = doc.Embedding
		metadatas[i] = doc.Metadata
		if metadatas[i] == nil {
			metadatas[i] = map[string]interface{}{}
		}
	}

	body := map[string]interface{}{
		"ids":        ids,
		"documents":  documents,
		"embeddings": embeddings,
		"metadatas":  metadatas,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal add documents request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v2/tenants/default_tenant/databases/default_database/collections/%s/add", c.baseURL, collectionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("add documents request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("add documents to %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.readError(resp, "add documents to %q", collection)
	}
	return nil
}

// UpsertDocuments adds or updates documents in a collection. This is idempotent —
// documents with the same ID are overwritten rather than duplicated.
func (c *ChromaClient) UpsertDocuments(ctx context.Context, collection string, docs []Document) error {
	if len(docs) == 0 {
		return nil
	}

	collectionID, err := c.getCollectionID(ctx, collection)
	if err != nil {
		return fmt.Errorf("upsert documents to %q: %w", collection, err)
	}

	ids := make([]string, len(docs))
	documents := make([]string, len(docs))
	embeddings := make([][]float64, len(docs))
	metadatas := make([]map[string]interface{}, len(docs))

	for i, doc := range docs {
		ids[i] = doc.ID
		documents[i] = doc.Content
		embeddings[i] = doc.Embedding
		metadatas[i] = doc.Metadata
		if metadatas[i] == nil {
			metadatas[i] = map[string]interface{}{}
		}
	}

	body := map[string]interface{}{
		"ids":        ids,
		"documents":  documents,
		"embeddings": embeddings,
		"metadatas":  metadatas,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal upsert documents request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v2/tenants/default_tenant/databases/default_database/collections/%s/upsert", c.baseURL, collectionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("upsert documents request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("upsert documents to %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.readError(resp, "upsert documents to %q", collection)
	}
	return nil
}

// QueryCollection queries a collection with a pre-computed embedding and returns the top K results.
func (c *ChromaClient) QueryCollection(ctx context.Context, collection string, queryEmbedding []float64, topK int) ([]QueryResult, error) {
	collectionID, err := c.getCollectionID(ctx, collection)
	if err != nil {
		return nil, fmt.Errorf("query collection %q: %w", collection, err)
	}

	body := map[string]interface{}{
		"query_embeddings": [][]float64{queryEmbedding},
		"n_results":        topK,
		"include":          []string{"documents", "metadatas", "distances", "embeddings"},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal query request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v2/tenants/default_tenant/databases/default_database/collections/%s/query", c.baseURL, collectionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("query collection request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query collection %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, c.readError(resp, "query collection %q", collection)
	}

	var result struct {
		IDs        [][]string                   `json:"ids"`
		Documents  [][]string                   `json:"documents"`
		Metadatas  [][]map[string]interface{}    `json:"metadatas"`
		Distances  [][]float64                  `json:"distances"`
		Embeddings [][][]float64                `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode query response: %w", err)
	}

	if len(result.IDs) == 0 || len(result.IDs[0]) == 0 {
		return nil, nil
	}

	queryResults := make([]QueryResult, len(result.IDs[0]))
	for i := range result.IDs[0] {
		doc := Document{
			ID:       result.IDs[0][i],
			Metadata: result.Metadatas[0][i],
		}
		if len(result.Documents) > 0 && len(result.Documents[0]) > i {
			doc.Content = result.Documents[0][i]
		}
		if len(result.Embeddings) > 0 && len(result.Embeddings[0]) > i {
			doc.Embedding = result.Embeddings[0][i]
		}

		distance := result.Distances[0][i]
		queryResults[i] = QueryResult{
			Document: doc,
			Distance: distance,
			Score:    1 - distance, // Convert distance to similarity score
		}
	}

	return queryResults, nil
}

// GetDocument retrieves a single document by ID from a collection.
func (c *ChromaClient) GetDocument(ctx context.Context, collection string, docID string) (Document, error) {
	collectionID, err := c.getCollectionID(ctx, collection)
	if err != nil {
		return Document{}, fmt.Errorf("get document from %q: %w", collection, err)
	}

	body := map[string]interface{}{
		"ids":     []string{docID},
		"include": []string{"documents", "metadatas", "embeddings"},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return Document{}, fmt.Errorf("marshal get document request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v2/tenants/default_tenant/databases/default_database/collections/%s/get", c.baseURL, collectionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return Document{}, fmt.Errorf("get document request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return Document{}, fmt.Errorf("get document from %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Document{}, c.readError(resp, "get document %q from %q", docID, collection)
	}

	var result struct {
		IDs        []string                   `json:"ids"`
		Documents  []string                   `json:"documents"`
		Metadatas  []map[string]interface{}    `json:"metadatas"`
		Embeddings [][]float64                `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Document{}, fmt.Errorf("decode get document response: %w", err)
	}

	if len(result.IDs) == 0 {
		return Document{}, fmt.Errorf("document %q not found in collection %q", docID, collection)
	}

	doc := Document{
		ID:       result.IDs[0],
		Metadata: result.Metadatas[0],
	}
	if len(result.Documents) > 0 {
		doc.Content = result.Documents[0]
	}
	if len(result.Embeddings) > 0 {
		doc.Embedding = result.Embeddings[0]
	}

	return doc, nil
}

// GetAllDocuments retrieves all documents from a collection.
func (c *ChromaClient) GetAllDocuments(ctx context.Context, collection string) ([]Document, error) {
	collectionID, err := c.getCollectionID(ctx, collection)
	if err != nil {
		return nil, fmt.Errorf("get all documents from %q: %w", collection, err)
	}

	body := map[string]interface{}{
		"include": []string{"documents", "metadatas"},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal get all documents request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v2/tenants/default_tenant/databases/default_database/collections/%s/get", c.baseURL, collectionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("get all documents request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get all documents from %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, c.readError(resp, "get all documents from %q", collection)
	}

	var result struct {
		IDs       []string                 `json:"ids"`
		Documents []string                 `json:"documents"`
		Metadatas []map[string]interface{} `json:"metadatas"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode get all documents response: %w", err)
	}

	docs := make([]Document, len(result.IDs))
	for i := range result.IDs {
		docs[i] = Document{
			ID:       result.IDs[i],
			Metadata: result.Metadatas[i],
		}
		if i < len(result.Documents) {
			docs[i].Content = result.Documents[i]
		}
	}

	return docs, nil
}

// UpdateDocument updates an existing document in a collection.
func (c *ChromaClient) UpdateDocument(ctx context.Context, collection string, doc Document) error {
	collectionID, err := c.getCollectionID(ctx, collection)
	if err != nil {
		return fmt.Errorf("update document in %q: %w", collection, err)
	}

	body := map[string]interface{}{
		"ids":        []string{doc.ID},
		"documents":  []string{doc.Content},
		"metadatas":  []map[string]interface{}{doc.Metadata},
	}
	if doc.Embedding != nil {
		body["embeddings"] = [][]float64{doc.Embedding}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal update document request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v2/tenants/default_tenant/databases/default_database/collections/%s/update", c.baseURL, collectionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("update document request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("update document in %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.readError(resp, "update document %q in %q", doc.ID, collection)
	}
	return nil
}

// DeleteDocument deletes a single document by ID from a collection.
func (c *ChromaClient) DeleteDocument(ctx context.Context, collection string, docID string) error {
	collectionID, err := c.getCollectionID(ctx, collection)
	if err != nil {
		return fmt.Errorf("delete document from %q: %w", collection, err)
	}

	body := map[string]interface{}{
		"ids": []string{docID},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal delete document request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v2/tenants/default_tenant/databases/default_database/collections/%s/delete", c.baseURL, collectionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("delete document request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("delete document from %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.readError(resp, "delete document %q from %q", docID, collection)
	}
	return nil
}

// CountDocuments returns the number of documents in a collection.
func (c *ChromaClient) CountDocuments(ctx context.Context, collection string) (int, error) {
	collectionID, err := c.getCollectionID(ctx, collection)
	if err != nil {
		return 0, fmt.Errorf("count documents in %q: %w", collection, err)
	}

	url := fmt.Sprintf("%s/api/v2/tenants/default_tenant/databases/default_database/collections/%s/count", c.baseURL, collectionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("count documents request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("count documents in %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, c.readError(resp, "count documents in %q", collection)
	}

	var count int
	if err := json.NewDecoder(resp.Body).Decode(&count); err != nil {
		return 0, fmt.Errorf("decode count response: %w", err)
	}
	return count, nil
}

// getCollectionID looks up a collection by name and returns its UUID.
func (c *ChromaClient) getCollectionID(ctx context.Context, name string) (string, error) {
	url := fmt.Sprintf("%s/api/v2/tenants/default_tenant/databases/default_database/collections/%s", c.baseURL, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("get collection request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("get collection %q: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", c.readError(resp, "get collection %q", name)
	}

	var col struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&col); err != nil {
		return "", fmt.Errorf("decode collection response: %w", err)
	}
	return col.ID, nil
}

// readError reads the response body and returns a formatted error.
func (c *ChromaClient) readError(resp *http.Response, format string, args ...interface{}) error {
	body, _ := io.ReadAll(resp.Body)
	msg := fmt.Sprintf(format, args...)
	return fmt.Errorf("%s: HTTP %d: %s", msg, resp.StatusCode, string(body))
}
