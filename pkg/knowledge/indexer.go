package knowledge

import (
	"context"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

const DefaultIndexBatchSize = 32

type IndexerConfig struct {
	Embedder  llm.Embedder
	Store     VectorStore
	Profile   string
	Namespace string
	BatchSize int
	Chunker   Chunker
}

type Indexer struct {
	embedder  llm.Embedder
	store     VectorStore
	profile   string
	namespace string
	batchSize int
	chunker   Chunker
}

type IndexResult struct {
	Documents int `json:"documents"`
	Chunks    int `json:"chunks"`
}

func NewIndexer(config IndexerConfig) (*Indexer, error) {
	if config.Embedder == nil {
		return nil, fmt.Errorf("knowledge indexer: embedder is nil")
	}
	if config.Store == nil {
		return nil, fmt.Errorf("knowledge indexer: vector store is nil")
	}
	if config.Profile == "" {
		return nil, fmt.Errorf("knowledge indexer: embedding profile is required")
	}
	batchSize := config.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultIndexBatchSize
	}
	chunker := config.Chunker
	if chunker == nil {
		var err error
		chunker, err = NewTextSplitter(TextSplitterConfig{})
		if err != nil {
			return nil, err
		}
	}
	return &Indexer{embedder: config.Embedder, store: config.Store, profile: config.Profile, namespace: config.Namespace, batchSize: batchSize, chunker: chunker}, nil
}

func (i *Indexer) Index(ctx context.Context, documents []Document) (IndexResult, error) {
	if err := ctx.Err(); err != nil {
		return IndexResult{}, err
	}
	chunks := make([]Document, 0, len(documents))
	for _, document := range documents {
		if document.Namespace == "" {
			document.Namespace = i.namespace
		}
		documentChunks, err := i.chunker.Split(document)
		if err != nil {
			return IndexResult{}, err
		}
		chunks = append(chunks, documentChunks...)
	}
	for start := 0; start < len(chunks); start += i.batchSize {
		end := start + i.batchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		if err := i.indexBatch(ctx, chunks[start:end]); err != nil {
			return IndexResult{}, err
		}
	}
	return IndexResult{Documents: len(documents), Chunks: len(chunks)}, nil
}

func (i *Indexer) indexBatch(ctx context.Context, chunks []Document) error {
	input := make([]string, len(chunks))
	for index, chunk := range chunks {
		input[index] = chunk.Content
	}
	vectors, err := i.embedder.Embed(ctx, i.profile, input)
	if err != nil {
		return err
	}
	if len(vectors) != len(chunks) {
		return fmt.Errorf("knowledge indexer: embedding response count %d did not match chunk count %d", len(vectors), len(chunks))
	}
	embeddings := make([]DocumentEmbedding, len(chunks))
	for index, chunk := range chunks {
		embeddings[index] = DocumentEmbedding{Document: chunk, Vector: append([]float32(nil), vectors[index]...)}
	}
	if err := i.store.Upsert(ctx, embeddings); err != nil {
		return fmt.Errorf("knowledge indexer: upsert batch: %w", err)
	}
	return nil
}
