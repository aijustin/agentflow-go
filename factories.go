package agentflow

import (
	"database/sql"
	"fmt"
	"time"

	blobfile "github.com/aijustin/agentflow-go/internal/adapter/blob/file"
	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	memoryfile "github.com/aijustin/agentflow-go/internal/adapter/memory/file"
	runstatefile "github.com/aijustin/agentflow-go/internal/adapter/runstate/file"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	runstatepostgres "github.com/aijustin/agentflow-go/internal/adapter/runstate/postgres"
	runstateredis "github.com/aijustin/agentflow-go/internal/adapter/runstate/redis"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

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

