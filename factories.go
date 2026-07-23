package agentflow

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	blobfile "github.com/aijustin/agentflow-go/internal/adapter/blob/file"
	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	"github.com/aijustin/agentflow-go/internal/adapter/llm/anthropic"
	"github.com/aijustin/agentflow-go/internal/adapter/llm/local"
	"github.com/aijustin/agentflow-go/internal/adapter/llm/openai"
	llmrouter "github.com/aijustin/agentflow-go/internal/adapter/llm/router"
	memoryfile "github.com/aijustin/agentflow-go/internal/adapter/memory/file"
	runstatefile "github.com/aijustin/agentflow-go/internal/adapter/runstate/file"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	runstatepostgres "github.com/aijustin/agentflow-go/internal/adapter/runstate/postgres"
	runstateredis "github.com/aijustin/agentflow-go/internal/adapter/runstate/redis"
	"github.com/aijustin/agentflow-go/pkg/catalog"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// --- Run-State, Blob, and Memory Stores ---

// NewInMemoryRunStateRepository creates the default in-memory run-state
// repository used by New.
func NewInMemoryRunStateRepository() runstate.Repository {
	return runstateinmem.NewRepository()
}

// NewInMemoryCheckpointHistory creates an append-only in-memory checkpoint history store.
func NewInMemoryCheckpointHistory() runstate.CheckpointHistory {
	return runstateinmem.NewCheckpointHistory()
}

// NewPostgresCheckpointHistory creates a PostgreSQL append-only checkpoint history store.
func NewPostgresCheckpointHistory(db *sql.DB, tableName ...string) (runstate.CheckpointHistory, error) {
	if len(tableName) > 1 {
		return nil, fmt.Errorf("agentflow: at most one postgres checkpoint history table name is allowed")
	}
	if len(tableName) == 1 && tableName[0] != "" {
		return runstatepostgres.NewCheckpointHistory(db, runstatepostgres.WithCheckpointHistoryTable(tableName[0]))
	}
	return runstatepostgres.NewCheckpointHistory(db)
}

// NewInMemoryBlobStore creates the default in-memory blob store used by New.
func NewInMemoryBlobStore() runstate.BlobStore {
	return blobinmem.NewStore()
}

// NewFileRunStateRepository creates a JSON-file-backed run-state repository.
func NewFileRunStateRepository(dir string) (runstate.Repository, error) {
	return runstatefile.NewRepository(dir)
}

// NewPostgresRunStateRepository creates a PostgreSQL-compatible run-state
// repository using a caller-provided *sql.DB. Applications must import and
// register their preferred PostgreSQL database/sql driver.
func NewPostgresRunStateRepository(db *sql.DB, tableName ...string) (runstate.Repository, error) {
	if len(tableName) > 1 {
		return nil, fmt.Errorf("agentflow: at most one postgres run-state table name is allowed")
	}
	if len(tableName) == 1 && tableName[0] != "" {
		return runstatepostgres.NewRepository(db, runstatepostgres.WithTableName(tableName[0]))
	}
	return runstatepostgres.NewRepository(db)
}

type RedisRunStateRepositoryConfig struct {
	Addr         string
	Password     string
	DB           int
	KeyPrefix    string
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// NewRedisRunStateRepository creates a Redis-backed run-state repository with
// compare-and-swap version checks for distributed workers.
func NewRedisRunStateRepository(config RedisRunStateRepositoryConfig) (runstate.Repository, error) {
	return runstateredis.NewRepository(runstateredis.Config{
		Addr:         config.Addr,
		Password:     config.Password,
		DB:           config.DB,
		KeyPrefix:    config.KeyPrefix,
		DialTimeout:  config.DialTimeout,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
	})
}

// NewFileBlobStore creates a file-backed blob store.
func NewFileBlobStore(dir string) (runstate.BlobStore, error) {
	return blobfile.NewStore(dir)
}

// NewFileMemoryRepository creates a JSON-file-backed memory repository.
func NewFileMemoryRepository(dir string) (memory.Repository, error) {
	return memoryfile.NewRepository(dir)
}

// --- Catalog Manifests ---

// LoadToolManifestFile loads and validates a standalone tool catalog manifest.
func LoadToolManifestFile(path string) (core.Tool, error) {
	return catalog.LoadToolManifestFile(path)
}

// LoadToolManifest loads and validates a standalone tool catalog manifest document.
func LoadToolManifest(data []byte) (core.Tool, error) {
	return catalog.LoadToolManifest(data)
}

// ValidateToolManifest validates a tool manifest for catalog registration.
func ValidateToolManifest(tool core.Tool) error {
	return catalog.ValidateToolManifest(tool)
}

// LoadSkillManifestFile loads and validates a standalone skill catalog manifest.
func LoadSkillManifestFile(path string) (core.Skill, error) {
	return catalog.LoadSkillManifestFile(path)
}

// LoadSkillManifest loads and validates a standalone skill catalog manifest document.
func LoadSkillManifest(data []byte) (core.Skill, error) {
	return catalog.LoadSkillManifest(data)
}

// ValidateSkillManifest validates a skill manifest for catalog registration.
func ValidateSkillManifest(skill core.Skill) error {
	return catalog.ValidateSkillManifest(skill)
}

// --- LLM Providers ---

type OpenAICompatibleProvider interface {
	llm.Gateway
	llm.Embedder
}

type LLMProviderRouter interface {
	llm.Gateway
	llm.Embedder
}

// NewOpenAICompatibleGateway creates a gateway for OpenAI-compatible chat APIs.
func NewOpenAICompatibleGateway(profiles []llm.Profile, client *http.Client) llm.Gateway {
	return openai.NewGateway(profiles, client)
}

// NewOpenAICompatibleProvider creates a gateway/embedder for OpenAI-compatible APIs.
func NewOpenAICompatibleProvider(profiles []llm.Profile, client *http.Client) OpenAICompatibleProvider {
	return openai.NewGateway(profiles, client)
}

// NewOpenAICompatibleEmbedder creates an embedder for OpenAI-compatible embedding APIs.
func NewOpenAICompatibleEmbedder(profiles []llm.Profile, client *http.Client) llm.Embedder {
	return openai.NewGateway(profiles, client)
}

// NewLocalGateway creates a gateway for local OpenAI-compatible model servers.
func NewLocalGateway(profiles []llm.Profile, client *http.Client) llm.Gateway {
	return local.NewGateway(profiles, client)
}

// NewAnthropicGateway creates a gateway for Anthropic Messages APIs.
func NewAnthropicGateway(profiles []llm.Profile, client *http.Client) llm.Gateway {
	return anthropic.NewGateway(profiles, client)
}

// NewLLMRouter routes profile names to provider-specific gateways.
func NewLLMRouter(routes map[string]llm.Gateway) llm.Gateway {
	return llmrouter.New(routes)
}

// NewLLMProviderRouter routes chat/tool/structured/streaming and embedding
// calls by profile name when the selected route supports the requested capability.
func NewLLMProviderRouter(routes map[string]llm.Gateway) LLMProviderRouter {
	return llmrouter.New(routes)
}

// --- Mock Gateway ---

// NewMockLLMGateway creates a fallback mock gateway for tests and examples.
func NewMockLLMGateway(scenario core.Scenario) llm.Gateway {
	gateway := llmmock.NewFallbackGateway()
	for name, ref := range scenario.LLMs {
		if ref.Provider != "mock" {
			continue
		}
		gateway.SetCapabilities(name, llm.CapChat, llm.CapToolCall, llm.CapStructuredOutput, llm.CapStream, llm.CapEmbed)
	}
	return gateway
}
