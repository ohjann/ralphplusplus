package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/eoghanhynes/ralph/internal/debuglog"
	"net/http"
	"sort"
	"strings"
	"time"
)

// CollectionCaps defines the maximum number of documents per collection.
var CollectionCaps = map[string]int{
	CollectionPatterns.Name:    CollectionPatterns.MaxDocuments,
	CollectionCompletions.Name: CollectionCompletions.MaxDocuments,
	CollectionErrors.Name:      CollectionErrors.MaxDocuments,
	CollectionDecisions.Name:   CollectionDecisions.MaxDocuments,
	CollectionCodebase.Name:    CollectionCodebase.MaxDocuments,
}

// DeduplicateInsert inserts a document into a collection with deduplication.
// If an existing document has cosine similarity > 0.9 (distance < 0.1), the
// existing document is updated instead: content is merged, timestamp refreshed,
// and the higher relevance_score is kept.
func DeduplicateInsert(ctx context.Context, client *ChromaClient, collection string, doc Document) error {
	if doc.Embedding == nil {
		return client.AddDocuments(ctx, collection, []Document{doc})
	}

	results, err := client.QueryCollection(ctx, collection, doc.Embedding, 1)
	if err != nil {
		// Collection may be empty or not exist yet — just insert
		return client.AddDocuments(ctx, collection, []Document{doc})
	}

	if len(results) > 0 && results[0].Distance < 0.1 {
		existing := results[0].Document
		merged := mergeDocuments(existing, doc)
		debuglog.Log("[memory/hygiene] dedup merge: existing=%s new=%s collection=%s", existing.ID, doc.ID, collection)
		return client.UpdateDocument(ctx, collection, merged)
	}

	return client.AddDocuments(ctx, collection, []Document{doc})
}

// DeduplicateInsertBatch inserts multiple documents with deduplication,
// then enforces the collection cap.
func DeduplicateInsertBatch(ctx context.Context, client *ChromaClient, collection string, docs []Document) error {
	for _, doc := range docs {
		if err := DeduplicateInsert(ctx, client, collection, doc); err != nil {
			return fmt.Errorf("deduplicate insert in %q: %w", collection, err)
		}
	}

	if cap, ok := CollectionCaps[collection]; ok {
		if err := EnforceCollectionCap(ctx, client, collection, cap); err != nil {
			return fmt.Errorf("enforce cap on %q: %w", collection, err)
		}
	}

	return nil
}

// EnforceCollectionCap ensures a collection does not exceed maxDocs documents.
// If the count exceeds maxDocs, the lowest relevance_score documents are deleted.
func EnforceCollectionCap(ctx context.Context, client *ChromaClient, collection string, maxDocs int) error {
	count, err := client.CountDocuments(ctx, collection)
	if err != nil {
		return fmt.Errorf("count documents in %q: %w", collection, err)
	}

	if count <= maxDocs {
		return nil
	}

	excess := count - maxDocs

	docs, err := getAllDocuments(ctx, client, collection)
	if err != nil {
		return fmt.Errorf("get all documents from %q: %w", collection, err)
	}

	// Sort by relevance_score ascending (lowest first)
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].RelevanceScore() < docs[j].RelevanceScore()
	})

	toDelete := excess
	if toDelete > len(docs) {
		toDelete = len(docs)
	}

	for i := 0; i < toDelete; i++ {
		if err := client.DeleteDocument(ctx, collection, docs[i].ID); err != nil {
			return fmt.Errorf("delete document %q from %q: %w", docs[i].ID, collection, err)
		}
	}

	debuglog.Log("[memory/hygiene] cap enforcement: evicted %d documents from %s (cap=%d)", toDelete, collection, maxDocs)
	return nil
}

// mergeDocuments merges a new document into an existing one.
// Content is combined, timestamp refreshed, and the higher relevance_score kept.
func mergeDocuments(existing, incoming Document) Document {
	merged := Document{
		ID:        existing.ID,
		Embedding: existing.Embedding,
		Metadata:  make(map[string]interface{}),
	}

	// Copy existing metadata
	for k, v := range existing.Metadata {
		merged.Metadata[k] = v
	}
	// Overlay incoming metadata
	for k, v := range incoming.Metadata {
		merged.Metadata[k] = v
	}

	// Merge content: if they differ, combine; otherwise keep existing
	if existing.Content == incoming.Content {
		merged.Content = existing.Content
	} else {
		merged.Content = existing.Content + "\n\n" + incoming.Content
	}

	// Keep the higher relevance_score
	existingScore := existing.RelevanceScore()
	incomingScore := incoming.RelevanceScore()
	if existingScore > incomingScore {
		merged.Metadata["relevance_score"] = existingScore
	} else {
		merged.Metadata["relevance_score"] = incomingScore
	}

	// Refresh timestamp
	merged.Metadata["last_confirmed"] = time.Now().UTC().Format(time.RFC3339)

	// Use incoming embedding if available (it's fresher)
	if incoming.Embedding != nil {
		merged.Embedding = incoming.Embedding
	}

	return merged
}

// getAllDocuments retrieves all documents from a collection using ChromaDB's get endpoint.
func getAllDocuments(ctx context.Context, client *ChromaClient, collection string) ([]Document, error) {
	collectionID, err := client.getCollectionID(ctx, collection)
	if err != nil {
		return nil, fmt.Errorf("get collection %q: %w", collection, err)
	}

	url := fmt.Sprintf("%s/api/v2/tenants/default_tenant/databases/default_database/collections/%s/get", client.baseURL, collectionID)

	body := strings.NewReader(`{"include":["documents","metadatas"]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("get all documents request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get all documents from %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, client.readError(resp, "get all documents from %q", collection)
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
