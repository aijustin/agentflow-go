package adapters

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	blobfile "github.com/aijustin/agentflow-go/internal/adapter/blob/file"
	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	blobs3 "github.com/aijustin/agentflow-go/internal/adapter/blob/s3"
	memoryfile "github.com/aijustin/agentflow-go/internal/adapter/memory/file"
	queueinmem "github.com/aijustin/agentflow-go/internal/adapter/queue/inmem"
	queuepostgres "github.com/aijustin/agentflow-go/internal/adapter/queue/postgres"
	runstatefile "github.com/aijustin/agentflow-go/internal/adapter/runstate/file"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	runstatepostgres "github.com/aijustin/agentflow-go/internal/adapter/runstate/postgres"
	runstateredis "github.com/aijustin/agentflow-go/internal/adapter/runstate/redis"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// --- Run-State and Checkpoint Stores ---

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

// --- Blob and Memory Stores ---

// NewInMemoryBlobStore creates the default in-memory blob store used by New.
func NewInMemoryBlobStore() runstate.BlobStore {
	return blobinmem.NewStore()
}

// NewFileBlobStore creates a file-backed blob store.
func NewFileBlobStore(dir string) (runstate.BlobStore, error) {
	return blobfile.NewStore(dir)
}

// NewFileMemoryRepository creates a JSON-file-backed memory repository.
func NewFileMemoryRepository(dir string) (memory.Repository, error) {
	return memoryfile.NewRepository(dir)
}

type S3BlobStoreConfig struct {
	Endpoint        string
	Bucket          string
	Region          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	HTTPClient      *http.Client

	// MaxObjectBytes caps how much of a single object a read buffers in
	// memory. Zero uses the adapter default.
	MaxObjectBytes int64
}

// NewS3BlobStore creates an S3-compatible blob store for large runtime and
// workflow outputs. It uses path-style object URLs, AWS Signature Version 4,
// and supports providers whose S3-compatible PUT/GET behavior has been tested.
func NewS3BlobStore(config S3BlobStoreConfig) (runstate.BlobStore, error) {
	return blobs3.NewStore(blobs3.Config{
		Endpoint:        config.Endpoint,
		Bucket:          config.Bucket,
		Region:          config.Region,
		Prefix:          config.Prefix,
		AccessKeyID:     config.AccessKeyID,
		SecretAccessKey: config.SecretAccessKey,
		SessionToken:    config.SessionToken,
		Client:          config.HTTPClient,
		MaxObjectBytes:  config.MaxObjectBytes,
	})
}

// --- Job Queues ---

func NewInMemoryJobQueue() asyncpkg.Queue {
	return queueinmem.NewQueue()
}

func NewPostgresJobQueue(db *sql.DB, tableName ...string) (asyncpkg.Queue, error) {
	if len(tableName) > 1 {
		return nil, fmt.Errorf("agentflow: at most one postgres job queue table name is allowed")
	}
	if len(tableName) == 1 && tableName[0] != "" {
		return queuepostgres.NewQueue(db, queuepostgres.WithTableName(tableName[0]))
	}
	return queuepostgres.NewQueue(db)
}
