package agentflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	asynchttp "github.com/aijustin/agentflow-go/internal/adapter/async/http"
	queueinmem "github.com/aijustin/agentflow-go/internal/adapter/queue/inmem"
	queuepostgres "github.com/aijustin/agentflow-go/internal/adapter/queue/postgres"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type AsyncRunHTTPHandlerConfig struct {
	Queue        asyncpkg.Queue
	Policy       security.Policy
	Audit        audit.Sink
	IDGenerator  func() string
	Now          func() time.Time
	MaxBodyBytes int64
}

type FrameworkRunJobHandlerConfig struct {
	Framework *Framework
}

type frameworkRunJobHandler struct {
	framework *Framework
}

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

func NewAsyncRunHTTPHandler(config AsyncRunHTTPHandlerConfig) (http.Handler, error) {
	return asynchttp.NewHandler(asynchttp.HandlerConfig{
		Queue:        config.Queue,
		Policy:       config.Policy,
		Audit:        config.Audit,
		IDGenerator:  config.IDGenerator,
		Now:          config.Now,
		MaxBodyBytes: config.MaxBodyBytes,
	})
}

func NewFrameworkRunJobHandler(config FrameworkRunJobHandlerConfig) (asyncpkg.Handler, error) {
	if config.Framework == nil {
		return nil, fmt.Errorf("agentflow: framework is nil")
	}
	return &frameworkRunJobHandler{framework: config.Framework}, nil
}

func (handler *frameworkRunJobHandler) HandleJob(ctx context.Context, job asyncpkg.Job) error {
	if job.Type != asyncpkg.RunJobType {
		return fmt.Errorf("agentflow: unsupported async job type %q", job.Type)
	}
	var payload asyncpkg.RunPayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("agentflow: decode run job payload: %w", err)
		}
	}
	if payload.RunID == "" {
		payload.RunID = job.RunID
	}
	if payload.RunID == "" {
		payload.RunID = job.ID
	}
	if err := payload.Principal.Validate(); err == nil {
		ctx = identity.WithPrincipal(ctx, payload.Principal)
	}
	_, err := handler.framework.Run(ctx, RunRequest{RunID: payload.RunID, Agent: payload.Agent, Prompt: payload.Prompt, Context: payload.Context})
	return err
}
