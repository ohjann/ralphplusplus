package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/eoghanhynes/ralph/internal/debuglog"
)

// ConfirmationTracker tracks which document IDs were retrieved and led to
// successful story completions during a PRD run. It is stored in-memory
// for the duration of the run.
type ConfirmationTracker struct {
	mu        sync.Mutex
	confirmed map[string]bool // key: "collection:docID"
}

// NewConfirmationTracker creates a new empty confirmation tracker.
func NewConfirmationTracker() *ConfirmationTracker {
	return &ConfirmationTracker{
		confirmed: make(map[string]bool),
	}
}

// ConfirmDocument marks a document as confirmed — it was retrieved and
// contributed to a successful story completion.
func (ct *ConfirmationTracker) ConfirmDocument(ctx context.Context, collection string, docID string) error {
	if collection == "" || docID == "" {
		return fmt.Errorf("confirm document: collection and docID must be non-empty")
	}
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.confirmed[collection+":"+docID] = true
	return nil
}

// IsConfirmed returns whether a document has been confirmed.
func (ct *ConfirmationTracker) IsConfirmed(collection, docID string) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.confirmed[collection+":"+docID]
}

// MaintenanceSummary holds per-collection stats from a decay cycle.
type MaintenanceSummary struct {
	Confirmed int
	Decayed   int
	Evicted   int
}

// RunDecayCycle runs the end-of-PRD maintenance routine:
//  1. For confirmed documents: bump relevance_score by 0.1 (capped at 2.0), update last_confirmed
//  2. For unconfirmed documents: multiply relevance_score by 0.85
//  3. Evict any documents with relevance_score below 0.3
func RunDecayCycle(ctx context.Context, client *ChromaClient, tracker *ConfirmationTracker) (MaintenanceSummary, error) {
	var total MaintenanceSummary
	collections := AllCollections()

	for _, col := range collections {
		summary, err := decayCollection(ctx, client, tracker, col.Name)
		if err != nil {
			debuglog.Log("[memory/maintenance] error processing collection %s: %v", col.Name, err)
			continue
		}
		debuglog.Log("[memory/maintenance] %s: confirmed=%d decayed=%d evicted=%d",
			col.Name, summary.Confirmed, summary.Decayed, summary.Evicted)
		total.Confirmed += summary.Confirmed
		total.Decayed += summary.Decayed
		total.Evicted += summary.Evicted
	}

	return total, nil
}

// decayCollection processes a single collection's decay cycle.
func decayCollection(ctx context.Context, client *ChromaClient, tracker *ConfirmationTracker, collection string) (MaintenanceSummary, error) {
	var summary MaintenanceSummary

	docs, err := getAllDocuments(ctx, client, collection)
	if err != nil {
		return summary, fmt.Errorf("get documents from %q: %w", collection, err)
	}

	if len(docs) == 0 {
		return summary, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var toEvict []string

	for _, doc := range docs {
		score := doc.RelevanceScore()
		if score == 0 {
			score = 1.0 // Default score for documents without one
		}

		if tracker.IsConfirmed(collection, doc.ID) {
			// Confirmed: bump by 0.1, cap at 2.0
			score += 0.1
			if score > 2.0 {
				score = 2.0
			}
			if doc.Metadata == nil {
				doc.Metadata = make(map[string]interface{})
			}
			doc.Metadata["relevance_score"] = score
			doc.Metadata["last_confirmed"] = now

			if err := client.UpdateDocument(ctx, collection, doc); err != nil {
				return summary, fmt.Errorf("update confirmed doc %q: %w", doc.ID, err)
			}
			summary.Confirmed++
		} else {
			// Unconfirmed: decay by 0.85
			score *= 0.85
			if doc.Metadata == nil {
				doc.Metadata = make(map[string]interface{})
			}
			doc.Metadata["relevance_score"] = score

			if score < 0.3 {
				toEvict = append(toEvict, doc.ID)
			} else {
				if err := client.UpdateDocument(ctx, collection, doc); err != nil {
					return summary, fmt.Errorf("update decayed doc %q: %w", doc.ID, err)
				}
			}
			summary.Decayed++
		}
	}

	// Evict stale documents
	for _, id := range toEvict {
		if err := client.DeleteDocument(ctx, collection, id); err != nil {
			return summary, fmt.Errorf("evict doc %q: %w", id, err)
		}
		summary.Evicted++
	}

	return summary, nil
}
