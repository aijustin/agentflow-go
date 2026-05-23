package coldindex

import (
	"context"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/knowledge"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

type Config struct {
	Embedder   llm.Embedder
	Store      knowledge.VectorStore
	Profile    string
	MemoryName string
}

type Indexer struct {
	embedder   llm.Embedder
	store      knowledge.VectorStore
	profile    string
	memoryName string
}

func NewIndexer(config Config) (*Indexer, error) {
	if config.Embedder == nil {
		return nil, fmt.Errorf("cold index: embedder is nil")
	}
	if config.Store == nil {
		return nil, fmt.Errorf("cold index: vector store is nil")
	}
	if config.Profile == "" {
		return nil, fmt.Errorf("cold index: embedding profile is required")
	}
	if config.MemoryName == "" {
		config.MemoryName = "session"
	}
	return &Indexer{
		embedder:   config.Embedder,
		store:      config.Store,
		profile:    config.Profile,
		memoryName: config.MemoryName,
	}, nil
}

func (i *Indexer) UpsertSummary(ctx context.Context, ns memory.Namespace, record tier.Record, summary string) error {
	vectors, err := i.embedder.Embed(ctx, i.profile, []string{summary})
	if err != nil {
		return err
	}
	if len(vectors) == 0 {
		return fmt.Errorf("cold index: empty embedding for record %q", record.ID)
	}
	namespace := coldNamespace(ns, i.memoryName)
	doc := knowledge.Document{
		ID:        record.ID,
		Content:   summary,
		Namespace: namespace,
		Metadata: map[string]string{
			"source": "memory_cold",
			"memory": i.memoryName,
			"tier":   string(tier.LevelCold),
		},
	}
	return i.store.Upsert(ctx, []knowledge.DocumentEmbedding{{Document: doc, Vector: vectors[0]}})
}

func (i *Indexer) SearchSummaries(ctx context.Context, ns memory.Namespace, query string, limit int) ([]string, error) {
	query = strings.TrimSpace(query)
	if query == "" || limit <= 0 {
		return nil, nil
	}
	vectors, err := i.embedder.Embed(ctx, i.profile, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, nil
	}
	results, err := i.store.Query(ctx, knowledge.Query{
		Namespace: coldNamespace(ns, i.memoryName),
		Vector:    vectors[0],
		Limit:     limit,
		Filter: map[string]string{
			"source": "memory_cold",
			"memory": i.memoryName,
		},
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(results))
	for _, result := range results {
		if result.Document.ID != "" {
			ids = append(ids, result.Document.ID)
		}
	}
	return ids, nil
}

func (i *Indexer) DeleteSummary(ctx context.Context, ns memory.Namespace, recordID string) error {
	if recordID == "" {
		return nil
	}
	return i.store.Delete(ctx, knowledge.DeleteRequest{
		Namespace: coldNamespace(ns, i.memoryName),
		ID:        recordID,
	})
}

func coldNamespace(ns memory.Namespace, memoryName string) string {
	prefix := strings.TrimSpace(ns.KeyPrefix())
	if prefix == "" {
		return "memory/" + memoryName + "/cold"
	}
	return prefix + "/memory/" + memoryName + "/cold"
}

var _ tier.ColdSummaryIndexer = (*Indexer)(nil)
