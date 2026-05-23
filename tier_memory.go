package agentflow

import (
	"database/sql"

	tiercold "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/cold"
	tierblobcold "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/blobcold"
	coldindex "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/coldindex"
	tierllmsummary "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/llmsummary"
	tierinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/inmem"
	tierpostgres "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/postgres"
	"github.com/aijustin/agentflow-go/pkg/knowledge"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// PostgresTierWarmStoreConfig configures a Postgres-backed warm tier store.
type PostgresTierWarmStoreConfig struct {
	DB        *sql.DB
	TableName string
}

// NewPostgresTierWarmStore creates a warm-tier store backed by Postgres JSONB rows.
func NewPostgresTierWarmStore(config PostgresTierWarmStoreConfig) (tier.Store, error) {
	var opts []tierpostgres.Option
	if config.TableName != "" {
		opts = append(opts, tierpostgres.WithTableName(config.TableName))
	}
	return tierpostgres.NewStore(config.DB, opts...)
}

// NewFileTierColdStore creates a gzip JSON cold-tier store on the local filesystem.
func NewFileTierColdStore(dir string) (tier.Store, error) {
	return tiercold.NewStore(dir)
}

// BlobTierColdStoreConfig configures a blob-backed cold tier with a local index directory.
type BlobTierColdStoreConfig struct {
	Blobs    runstate.BlobStore
	IndexDir string
}

// NewBlobTierColdStore stores gzip JSON cold-tier records in a BlobAdmin backend.
func NewBlobTierColdStore(config BlobTierColdStoreConfig) (tier.Store, error) {
	return tierblobcold.NewStore(tierblobcold.Config{
		Blobs:    config.Blobs,
		IndexDir: config.IndexDir,
	})
}

// TierColdSummaryIndexerConfig configures vector indexing for cold-tier summaries.
type TierColdSummaryIndexerConfig struct {
	Embedder   llm.Embedder
	Store      knowledge.VectorStore
	Profile    string
	MemoryName string
}

// NewTierColdSummaryIndexer indexes cold-tier summaries for semantic recall.
func NewTierColdSummaryIndexer(config TierColdSummaryIndexerConfig) (tier.ColdSummaryIndexer, error) {
	return coldindex.NewIndexer(coldindex.Config{
		Embedder:   config.Embedder,
		Store:      config.Store,
		Profile:    config.Profile,
		MemoryName: config.MemoryName,
	})
}

// CompositeTierStoreConfig wires hot, warm, and cold tier backends.
type CompositeTierStoreConfig struct {
	Hot  tier.Store
	Warm tier.Store
	Cold tier.Store
}

// NewCompositeTierStore routes records across tier backends.
func NewCompositeTierStore(config CompositeTierStoreConfig) tier.Store {
	return &tier.CompositeStore{
		Hot:  config.Hot,
		Warm: config.Warm,
		Cold: config.Cold,
	}
}

// NewInMemoryTierHotStore creates an in-process hot-tier store.
func NewInMemoryTierHotStore() tier.Store {
	return tierinmem.NewStore()
}

// NewLLMTierSummarizer creates an LLM-backed cold-tier content summarizer.
func NewLLMTierSummarizer(gateway llm.Chatter, profile string) tier.ContentSummarizer {
	return tierllmsummary.NewSummarizer(gateway, profile)
}

// NewCognitiveTierMemory exposes a tier Manager through the CognitiveMemory port.
func NewCognitiveTierMemory(manager tier.Manager, weights tier.RecallWeights) memory.CognitiveMemory {
	return tier.NewCognitiveAdapter(manager, weights)
}
