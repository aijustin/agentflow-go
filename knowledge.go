package agentflow

import (
	"database/sql"
	"net/http"

	knowledgefile "github.com/aijustin/agentflow-go/internal/adapter/knowledge/file"
	knowledgehttp "github.com/aijustin/agentflow-go/internal/adapter/knowledge/http"
	toolretriever "github.com/aijustin/agentflow-go/internal/adapter/tool/retriever"
	vectorpostgres "github.com/aijustin/agentflow-go/internal/adapter/vector/postgres"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/knowledge"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

type RetrieverToolConfig struct {
	Embedder            llm.Embedder
	Store               knowledge.VectorStore
	Profile             string
	Namespace           string
	DefaultLimit        int
	SearchMode          knowledge.SearchMode
	CandidateMultiplier int
	Reranker            knowledge.Reranker
	VectorWeight        float64
	TextWeight          float64
}

type PostgresVectorStoreConfig struct {
	DB        *sql.DB
	TableName string
}

type FileKnowledgeLoaderConfig struct {
	Paths     []string
	Namespace string
	Metadata  map[string]string
	MaxBytes  int64
}

type HTTPKnowledgeLoaderConfig struct {
	URLs      []string
	Namespace string
	Metadata  map[string]string
	MaxBytes  int64
	Client    *http.Client
}

type KnowledgeIndexerConfig struct {
	Embedder  llm.Embedder
	Store     knowledge.VectorStore
	Profile   string
	Namespace string
	BatchSize int
	Chunker   knowledge.Chunker
}

// NewRetrieverTool creates a semantic retrieval tool backed by an embedder and vector store.
func NewRetrieverTool(config RetrieverToolConfig) (core.ToolExecutor, error) {
	return toolretriever.NewTool(toolretriever.Config{
		Embedder:            config.Embedder,
		Store:               config.Store,
		Profile:             config.Profile,
		Namespace:           config.Namespace,
		DefaultLimit:        config.DefaultLimit,
		SearchMode:          config.SearchMode,
		CandidateMultiplier: config.CandidateMultiplier,
		Reranker:            config.Reranker,
		VectorWeight:        config.VectorWeight,
		TextWeight:          config.TextWeight,
	})
}

// NewPostgresVectorStore creates a pgvector-compatible knowledge vector store.
func NewPostgresVectorStore(config PostgresVectorStoreConfig) (knowledge.VectorStore, error) {
	options := make([]vectorpostgres.Option, 0, 1)
	if config.TableName != "" {
		options = append(options, vectorpostgres.WithTableName(config.TableName))
	}
	return vectorpostgres.NewStore(config.DB, options...)
}

// NewFileKnowledgeLoader creates a filesystem document loader for knowledge ingestion.
func NewFileKnowledgeLoader(config FileKnowledgeLoaderConfig) (knowledge.Loader, error) {
	return knowledgefile.NewLoader(knowledgefile.Config{
		Paths:     config.Paths,
		Namespace: config.Namespace,
		Metadata:  config.Metadata,
		MaxBytes:  config.MaxBytes,
	})
}

// NewHTTPKnowledgeLoader creates an HTTP document loader for knowledge ingestion.
func NewHTTPKnowledgeLoader(config HTTPKnowledgeLoaderConfig) (knowledge.Loader, error) {
	return knowledgehttp.NewLoader(knowledgehttp.Config{
		URLs:      config.URLs,
		Namespace: config.Namespace,
		Metadata:  config.Metadata,
		MaxBytes:  config.MaxBytes,
		Client:    config.Client,
	})
}

// NewKnowledgeIndexer creates a document chunking, embedding, and vector upsert pipeline.
func NewKnowledgeIndexer(config KnowledgeIndexerConfig) (*knowledge.Indexer, error) {
	return knowledge.NewIndexer(knowledge.IndexerConfig{
		Embedder:  config.Embedder,
		Store:     config.Store,
		Profile:   config.Profile,
		Namespace: config.Namespace,
		BatchSize: config.BatchSize,
		Chunker:   config.Chunker,
	})
}
