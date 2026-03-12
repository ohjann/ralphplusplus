package memory

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const notSetupMsg = "Run ralph first to initialize memory, or run ralph memory reset to create empty collections"

// startSidecar ensures ChromaDB is set up and starts the sidecar.
// Returns the client, sidecar, and a cleanup function. The caller must call
// cleanup when done.
func startSidecar(ctx context.Context, dataDir string, port int) (*ChromaClient, *Sidecar, func(), error) {
	if port == 0 {
		port = 9876
	}

	pythonPath, err := EnsureChromaDB(dataDir, func(msg string) {
		fmt.Println(msg)
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s\n\n%s", err, notSetupMsg)
	}

	sidecar := &Sidecar{}
	if err := sidecar.Start(ctx, pythonPath, dataDir, port); err != nil {
		return nil, nil, nil, fmt.Errorf("starting ChromaDB: %w\n\n%s", err, notSetupMsg)
	}

	client := NewClient(fmt.Sprintf("http://localhost:%d", port))
	cleanup := func() { sidecar.Stop() }
	return client, sidecar, cleanup, nil
}

// RunStats prints collection sizes, total documents, average relevance scores,
// and the ChromaDB data directory size.
func RunStats(ctx context.Context, dataDir string, port int) error {
	client, _, cleanup, err := startSidecar(ctx, dataDir, port)
	if err != nil {
		return err
	}
	defer cleanup()

	collections := AllCollections()
	totalDocs := 0

	fmt.Println("Memory Statistics")
	fmt.Println("=================")
	fmt.Println()

	for _, col := range collections {
		count, err := client.CountDocuments(ctx, col.Name)
		if err != nil {
			fmt.Printf("  %-25s  error: %v\n", col.Name, err)
			continue
		}
		totalDocs += count

		avgScore := 0.0
		if count > 0 {
			docs, err := getAllDocuments(ctx, client, col.Name)
			if err == nil && len(docs) > 0 {
				sum := 0.0
				for _, d := range docs {
					s := d.RelevanceScore()
					if s == 0 {
						s = 1.0
					}
					sum += s
				}
				avgScore = sum / float64(len(docs))
			}
		}

		fmt.Printf("  %-25s  docs: %d/%d   avg_score: %.2f\n", col.Name, count, col.MaxDocuments, avgScore)
	}

	fmt.Println()
	fmt.Printf("Total documents: %d\n", totalDocs)

	// Print data directory size.
	chromaDir := filepath.Join(dataDir, "chroma")
	size, err := dirSize(chromaDir)
	if err != nil {
		fmt.Printf("Data directory: %s (unable to calculate size: %v)\n", chromaDir, err)
	} else {
		fmt.Printf("Data directory: %s (%s)\n", chromaDir, humanSize(size))
	}

	return nil
}

// RunSearch embeds a query and searches all collections, printing top-5 results per collection.
func RunSearch(ctx context.Context, dataDir string, port int, query string) error {
	client, _, cleanup, err := startSidecar(ctx, dataDir, port)
	if err != nil {
		return err
	}
	defer cleanup()

	embedder, err := NewAnthropicEmbedder()
	if err != nil {
		return fmt.Errorf("creating embedder: %w", err)
	}

	embedding, err := embedder.EmbedOne(ctx, query)
	if err != nil {
		return fmt.Errorf("embedding query: %w", err)
	}

	fmt.Printf("Search results for: %q\n", query)
	fmt.Println(strings.Repeat("=", 40))

	collections := AllCollections()
	anyResults := false

	for _, col := range collections {
		results, err := client.QueryCollection(ctx, col.Name, embedding, 5)
		if err != nil {
			fmt.Printf("\n[%s] error: %v\n", col.Name, err)
			continue
		}
		if len(results) == 0 {
			continue
		}

		fmt.Printf("\n[%s]\n", col.Name)
		for i, r := range results {
			preview := r.Document.Content
			if len(preview) > 100 {
				preview = preview[:100] + "..."
			}
			preview = strings.ReplaceAll(preview, "\n", " ")
			fmt.Printf("  %d. (score: %.3f) %s\n", i+1, r.Score, preview)
			anyResults = true
		}
	}

	if !anyResults {
		fmt.Println("\nNo results found.")
	}

	return nil
}

// RunPrune runs a decay cycle (decay all scores by 0.85, evict below 0.3)
// and prints a summary.
func RunPrune(ctx context.Context, dataDir string, port int) error {
	client, _, cleanup, err := startSidecar(ctx, dataDir, port)
	if err != nil {
		return err
	}
	defer cleanup()

	fmt.Println("Running memory prune (decay cycle)...")
	fmt.Println()

	collections := AllCollections()
	totalDecayed := 0
	totalEvicted := 0

	for _, col := range collections {
		// Use an empty tracker so all docs are treated as unconfirmed (decayed).
		tracker := NewConfirmationTracker()
		summary, err := decayCollection(ctx, client, tracker, col.Name)
		if err != nil {
			fmt.Printf("  %-25s  error: %v\n", col.Name, err)
			continue
		}
		totalDecayed += summary.Decayed
		totalEvicted += summary.Evicted
		fmt.Printf("  %-25s  decayed: %d  evicted: %d\n", col.Name, summary.Decayed, summary.Evicted)
	}

	fmt.Println()
	fmt.Printf("Total: %d documents decayed, %d documents evicted\n", totalDecayed, totalEvicted)
	return nil
}

// RunReset deletes all collections and recreates them empty, with a confirmation prompt.
func RunReset(ctx context.Context, dataDir string, port int) error {
	fmt.Print("Are you sure? This deletes all learned memories. y/n: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("no input received")
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if answer != "y" && answer != "yes" {
		fmt.Println("Aborted.")
		return nil
	}

	client, _, cleanup, err := startSidecar(ctx, dataDir, port)
	if err != nil {
		return err
	}
	defer cleanup()

	collections := AllCollections()

	fmt.Println("Resetting all memory collections...")
	for _, col := range collections {
		// Delete (ignore error if collection doesn't exist).
		_ = client.DeleteCollection(ctx, col.Name)

		if err := client.CreateCollection(ctx, col.Name); err != nil {
			return fmt.Errorf("recreating collection %s: %w", col.Name, err)
		}
		fmt.Printf("  %-25s  reset\n", col.Name)
	}

	fmt.Println()
	fmt.Println("All memory collections have been reset.")
	return nil
}

// dirSize calculates the total size of all files in a directory tree.
func dirSize(path string) (int64, error) {
	var total int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// humanSize formats bytes as a human-readable string.
func humanSize(bytes int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
