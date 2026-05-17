package knowledge

import "context"

type Document struct {
	ID        string            `json:"id"`
	Content   string            `json:"content"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Namespace string            `json:"namespace,omitempty"`
}

type DocumentEmbedding struct {
	Document Document  `json:"document"`
	Vector   []float32 `json:"vector"`
}

type Query struct {
	Namespace    string            `json:"namespace,omitempty"`
	Text         string            `json:"text,omitempty"`
	Mode         SearchMode        `json:"mode,omitempty"`
	Vector       []float32         `json:"vector"`
	Limit        int               `json:"limit,omitempty"`
	Filter       map[string]string `json:"filter,omitempty"`
	VectorWeight float64           `json:"vector_weight,omitempty"`
	TextWeight   float64           `json:"text_weight,omitempty"`
}

type SearchMode string

const (
	SearchModeVector SearchMode = "vector"
	SearchModeHybrid SearchMode = "hybrid"
)

type SearchResult struct {
	Document Document `json:"document"`
	Score    float64  `json:"score"`
}

type DeleteRequest struct {
	Namespace string `json:"namespace,omitempty"`
	ID        string `json:"id"`
}

type Loader interface {
	Load(ctx context.Context) ([]Document, error)
}

type VectorStore interface {
	Upsert(ctx context.Context, documents []DocumentEmbedding) error
	Query(ctx context.Context, query Query) ([]SearchResult, error)
	Delete(ctx context.Context, req DeleteRequest) error
}

type HybridSearcher interface {
	HybridQuery(ctx context.Context, query Query) ([]SearchResult, error)
}

type Reranker interface {
	Rerank(ctx context.Context, query string, results []SearchResult) ([]SearchResult, error)
}
