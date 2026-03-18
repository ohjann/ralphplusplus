package memory

import "context"

// Retriever wraps a ChromaClient and Embedder to provide semantic memory
// retrieval. It implements the runner.MemoryRetriever interface.
type Retriever struct {
	Client   *ChromaClient
	Embedder Embedder
	RepoID   string // if set, scopes retrieval to this repository
}

// NewRetriever creates a Retriever from a ChromaClient and Embedder.
// Returns nil if either dependency is nil.
func NewRetriever(client *ChromaClient, embedder Embedder) *Retriever {
	if client == nil || embedder == nil {
		return nil
	}
	return &Retriever{Client: client, Embedder: embedder}
}

// RetrieveContext queries all memory collections and returns a RetrievalResult
// for prompt injection. Delegates to the package-level RetrieveContext function.
// If the Retriever has a RepoID set, it is applied to the retrieval options.
func (r *Retriever) RetrieveContext(ctx context.Context, storyTitle, storyDescription string, acceptanceCriteria []string, opts RetrievalOptions) (RetrievalResult, error) {
	if opts.RepoID == "" && r.RepoID != "" {
		opts.RepoID = r.RepoID
	}
	return RetrieveContext(ctx, r.Client, r.Embedder, storyTitle, storyDescription, acceptanceCriteria, opts)
}
