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
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type AsyncRunHTTPHandlerConfig struct {
	Queue        asyncpkg.Queue
	RunState     runstate.Repository
	Policy       security.Policy
	Audit        audit.Sink
	IDGenerator  func() string
	Now          func() time.Time
	MaxBodyBytes int64
}

type FrameworkRunJobHandlerConfig struct {
	Framework *Framework
}

type frameworkJobHandler struct {
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
		RunState:     config.RunState,
		Policy:       config.Policy,
		Audit:        config.Audit,
		IDGenerator:  config.IDGenerator,
		Now:          config.Now,
		MaxBodyBytes: config.MaxBodyBytes,
	})
}

// NewFrameworkJobHandler executes framework run, event, resume.continue, and memory.reconcile jobs.
func NewFrameworkJobHandler(config FrameworkRunJobHandlerConfig) (asyncpkg.Handler, error) {
	if config.Framework == nil {
		return nil, fmt.Errorf("agentflow: framework is nil")
	}
	return &frameworkJobHandler{framework: config.Framework}, nil
}

func (handler *frameworkJobHandler) HandleJob(ctx context.Context, job asyncpkg.Job) error {
	switch job.Type {
	case asyncpkg.RunJobType:
		return handler.handleRun(ctx, job)
	case asyncpkg.EventJobType:
		return handler.handleEvent(ctx, job)
	case asyncpkg.ResumeContinueJobType:
		return handler.handleResumeContinue(ctx, job)
	case asyncpkg.MemoryReconcileJobType:
		return handler.handleMemoryReconcile(ctx, job)
	default:
		return fmt.Errorf("agentflow: unsupported async job type %q", job.Type)
	}
}

func (handler *frameworkJobHandler) handleRun(ctx context.Context, job asyncpkg.Job) error {
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
	ctx, err := withJobPrincipal(ctx, payload.Principal)
	if err != nil {
		return err
	}
	_, err = handler.framework.Run(ctx, RunRequest{
		RunID:   payload.RunID,
		Agent:   payload.Agent,
		Prompt:  payload.Prompt,
		Context: payload.Context,
	})
	return err
}

func (handler *frameworkJobHandler) handleEvent(ctx context.Context, job asyncpkg.Job) error {
	var payload asyncpkg.EventPayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("agentflow: decode event job payload: %w", err)
		}
	}
	ctx, err := withJobPrincipal(ctx, payload.Principal)
	if err != nil {
		return err
	}
	result, err := handler.framework.HandleEvent(ctx, payload.Event())
	if err != nil {
		return err
	}
	if result.Status == runstate.RunStatusPaused {
		return nil
	}
	return nil
}

func (handler *frameworkJobHandler) handleResumeContinue(ctx context.Context, job asyncpkg.Job) error {
	var payload asyncpkg.ResumeContinuePayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("agentflow: decode resume.continue job payload: %w", err)
		}
	}
	if payload.Token == "" || !payload.Decision.Valid() {
		return fmt.Errorf("agentflow: resume.continue job requires token and valid decision")
	}
	ctx, err := withJobPrincipal(ctx, payload.Principal)
	if err != nil {
		return err
	}
	_, err = handler.framework.ResumeAndContinue(ctx, payload.Token, payload.Decision, payload.Amendment)
	return err
}

func (handler *frameworkJobHandler) handleMemoryReconcile(ctx context.Context, job asyncpkg.Job) error {
	var payload asyncpkg.MemoryReconcilePayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("agentflow: decode memory.reconcile job payload: %w", err)
		}
	}
	if payload.MemoryName == "" || payload.Agent == "" {
		return fmt.Errorf("agentflow: memory.reconcile job requires memory_name and agent")
	}
	ctx, err := withJobPrincipal(ctx, payload.Principal)
	if err != nil {
		return err
	}
	runID := payload.RunID
	if runID == "" {
		runID = job.RunID
	}
	return handler.framework.engine.ReconcileTierMemory(ctx, runID, payload.MemoryName, payload.Agent)
}

func withJobPrincipal(ctx context.Context, principal identity.Principal) (context.Context, error) {
	if principal.ID == "" && principal.Type == "" && principal.Scope.TenantID == "" {
		return ctx, nil
	}
	if err := principal.Validate(); err != nil {
		return ctx, fmt.Errorf("agentflow: invalid async job principal: %w", err)
	}
	return identity.WithPrincipal(ctx, principal), nil
}
