package agentflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	asynchttp "github.com/aijustin/agentflow-go/internal/adapter/async/http"
	checkpointhttp "github.com/aijustin/agentflow-go/internal/adapter/checkpoint/http"
	eventrouterhttp "github.com/aijustin/agentflow-go/internal/adapter/eventrouter/http"
	humanhttp "github.com/aijustin/agentflow-go/internal/adapter/human/http"
	queueinmem "github.com/aijustin/agentflow-go/internal/adapter/queue/inmem"
	queuepostgres "github.com/aijustin/agentflow-go/internal/adapter/queue/postgres"
	retentionhttp "github.com/aijustin/agentflow-go/internal/adapter/retention/http"
	studiohttp "github.com/aijustin/agentflow-go/internal/adapter/studio/http"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

// --- Checkpoint ---

type CheckpointHTTPHandlerConfig struct {
	Framework    *Framework
	MaxBodyBytes int64
	// Policy authorizes requests: reads as run.read, writes (resume-from-step,
	// resume-from-checkpoint, fork) as hitl.resume / run.submit, with the
	// caller's tenant bound to the resource. When Policy is nil the write
	// endpoints default-deny with 403 auth_required — mounting them open was
	// a privilege-escalation hole (any caller could resume or fork any run).
	Policy security.Policy
	// Audit receives policy-denied records when configured.
	Audit audit.Sink
	// InsecureAllowNoAuth disables the default-deny protection on write
	// endpoints when Policy is nil. Only set it behind an authenticating
	// reverse proxy or in tests.
	InsecureAllowNoAuth bool
}

// NewCheckpointHTTPHandler serves production checkpoint routes:
//   - GET  /v1/runs/{run_id}/steps
//   - POST /v1/runs/{run_id}/resume-from-step
//   - GET  /v1/runs/{run_id}/checkpoints
//   - GET  /v1/runs/{run_id}/checkpoints/{version}
//   - POST /v1/runs/{run_id}/resume-from-checkpoint
//   - POST /v1/runs/{run_id}/fork
//
// Write routes require CheckpointHTTPHandlerConfig.Policy (or an explicit
// InsecureAllowNoAuth opt-out) since they mint fresh execution for a run.
func NewCheckpointHTTPHandler(config CheckpointHTTPHandlerConfig) (http.Handler, error) {
	if config.Framework == nil {
		return nil, fmt.Errorf("agentflow: checkpoint handler requires framework")
	}
	adapter := &studioFramework{framework: config.Framework}
	return checkpointhttp.NewHandler(checkpointhttp.HandlerConfig{
		Checkpoint:          adapter,
		Steps:               adapter,
		History:             adapter,
		Checkpoints:         adapter,
		Restore:             adapter,
		Fork:                adapter,
		MaxBodyBytes:        config.MaxBodyBytes,
		Policy:              config.Policy,
		Audit:               config.Audit,
		InsecureAllowNoAuth: config.InsecureAllowNoAuth,
	}), nil
}

// --- Retention ---

type RetentionHTTPHandlerConfig struct {
	Framework    *Framework
	Policy       security.Policy
	Audit        audit.Sink
	MaxBodyBytes int64
	// InsecureAllowNoAuth disables the default-deny guard on the purge
	// endpoints when Policy is nil. Only set it behind an authenticating
	// reverse proxy or in tests.
	InsecureAllowNoAuth bool
}

type retentionAdapter struct {
	framework *Framework
}

func (a *retentionAdapter) PurgeRuns(ctx context.Context, filter runstate.ListFilter) (int, error) {
	return a.framework.PurgeRuns(ctx, filter)
}

func (a *retentionAdapter) PurgeExpired(ctx context.Context, maxAge time.Duration) (int, error) {
	return a.framework.PurgeExpired(ctx, maxAge)
}

func (a *retentionAdapter) PurgeWithPolicy(ctx context.Context, policy retentionhttp.RetentionPolicy) (int, error) {
	return a.framework.PurgeWithPolicy(ctx, RetentionPolicy{
		MaxAge:       policy.MaxAge,
		Status:       policy.Status,
		ScenarioName: policy.ScenarioName,
		Limit:        policy.Limit,
	})
}

func (a *retentionAdapter) PurgeOrphanBlobs(ctx context.Context) (int, error) {
	return a.framework.PurgeOrphanBlobs(ctx)
}

// NewRetentionHTTPHandler serves admin retention routes:
//   - POST /v1/admin/retention/purge-runs
//   - POST /v1/admin/retention/purge-expired
//   - POST /v1/admin/retention/purge-policy
//   - POST /v1/admin/retention/purge-blobs
func NewRetentionHTTPHandler(config RetentionHTTPHandlerConfig) (http.Handler, error) {
	if config.Framework == nil {
		return nil, fmt.Errorf("agentflow: retention handler requires framework")
	}
	return retentionhttp.NewHandler(retentionhttp.HandlerConfig{
		Purger:              &retentionAdapter{framework: config.Framework},
		Policy:              config.Policy,
		Audit:               config.Audit,
		MaxBodyBytes:        config.MaxBodyBytes,
		InsecureAllowNoAuth: config.InsecureAllowNoAuth,
	})
}

// --- Studio ---

type StudioHTTPHandlerConfig struct {
	Framework      *Framework
	StudioSavePath string
	MaxBodyBytes   int64
	// Policy authorizes the mutating endpoints: studio run as run.submit and
	// studio save as admin.configure. When Policy is nil those endpoints
	// default-deny with 403 auth_required — studio run executes agents and
	// studio save rewrites the scenario file, so mounting them open was a
	// privilege-escalation hole. The pure-transform endpoints
	// (validate/codegen/yaml/import-yaml) stay open.
	Policy security.Policy
	// Audit receives policy-denied records when configured.
	Audit audit.Sink
	// InsecureAllowNoAuth disables the default-deny protection on the
	// mutating endpoints when Policy is nil. Only set it behind an
	// authenticating reverse proxy or in tests.
	InsecureAllowNoAuth bool
}

// NewStudioHTTPHandler serves production Studio routes:
//   - POST /v1/studio/validate
//   - POST /v1/studio/codegen
//   - POST /v1/studio/yaml
//   - POST /v1/studio/import-yaml
//   - POST /v1/studio/run
//   - POST /v1/studio/save (when StudioSavePath is set)
//
// The run/save routes require StudioHTTPHandlerConfig.Policy (or an explicit
// InsecureAllowNoAuth opt-out).
func NewStudioHTTPHandler(config StudioHTTPHandlerConfig) (http.Handler, error) {
	if config.Framework == nil {
		return nil, fmt.Errorf("agentflow: studio handler requires framework")
	}
	adapter := &studioFramework{framework: config.Framework, savePath: config.StudioSavePath}
	httpConfig := studiohttp.HandlerConfig{
		Validate:            adapter,
		Codegen:             adapter,
		YAML:                adapter,
		ImportYAML:          adapter,
		Run:                 adapter,
		MaxBodyBytes:        config.MaxBodyBytes,
		Policy:              config.Policy,
		Audit:               config.Audit,
		InsecureAllowNoAuth: config.InsecureAllowNoAuth,
	}
	if config.StudioSavePath != "" {
		httpConfig.Save = adapter
	}
	return studiohttp.NewHandler(httpConfig), nil
}

// --- Webhook & Human Gate ---

type WebhookHTTPHandlerConfig struct {
	Framework    *Framework
	MaxBodyBytes int64
	// VerifySignature, when set, validates the raw webhook body before the
	// event is decoded (e.g. an HMAC signature from the event source); a
	// non-nil error rejects the request with 401. Webhooks are
	// unauthenticated ingress — production deployments should set this or
	// wrap the handler in an authenticating middleware.
	VerifySignature func(r *http.Request, body []byte) error
}

type HumanHTTPHandlerConfig struct {
	Framework    *Framework
	MaxBodyBytes int64
}

type webhookFramework struct {
	framework *Framework
}

func (adapter *webhookFramework) HandleEvent(r *http.Request, event IncomingEvent) (any, error) {
	return adapter.framework.HandleEvent(r.Context(), event)
}

type humanFramework struct {
	framework *Framework
}

func (adapter *humanFramework) Resume(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) error {
	return adapter.framework.Resume(ctx, token, decision, amendment)
}

func (adapter *humanFramework) ResumeAndContinue(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) (any, error) {
	return adapter.framework.ResumeAndContinue(ctx, token, decision, amendment)
}

// NewWebhookHTTPHandler serves POST / requests that accept IncomingEvent JSON payloads.
func NewWebhookHTTPHandler(config WebhookHTTPHandlerConfig) (http.Handler, error) {
	if config.Framework == nil {
		return nil, fmt.Errorf("agentflow: webhook handler requires framework")
	}
	return eventrouterhttp.NewHandler(eventrouterhttp.HandlerConfig{
		Framework:       &webhookFramework{framework: config.Framework},
		MaxBodyBytes:    config.MaxBodyBytes,
		VerifySignature: eventrouterhttp.SignatureVerifier(config.VerifySignature),
	})
}

// NewHumanHTTPHandler serves human gate resume requests. When the request sets
// continue=true, the handler calls ResumeAndContinue instead of Resume.
// It panics when config.Framework is nil: a nil framework would panic later at
// request time inside the adapter, so failing fast at construction surfaces
// the wiring mistake where it is made.
func NewHumanHTTPHandler(config HumanHTTPHandlerConfig) http.Handler {
	if config.Framework == nil {
		panic("agentflow: NewHumanHTTPHandler requires a non-nil Framework")
	}
	adapter := &humanFramework{framework: config.Framework}
	return humanhttp.NewHandler(humanhttp.HandlerConfig{
		Gate:         adapter,
		Continuer:    adapter,
		MaxBodyBytes: config.MaxBodyBytes,
	})
}

// --- Async Jobs ---

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
	// A redelivered run job for a run that already Failed cannot re-run from
	// scratch (Run rejects it with ErrRunFailed, so every queue retry would
	// fail identically until dead-letter). Re-enter through RetryFailedRun,
	// which resumes from the persisted checkpoint instead.
	if snapshot, loadErr := runstate.LoadAuthorized(ctx, handler.framework.runs, payload.RunID); loadErr == nil &&
		snapshot.Status == runstate.RunStatusFailed {
		result, err := handler.framework.RetryFailedRun(ctx, payload.RunID)
		if err != nil {
			return err
		}
		if result.Status == runstate.RunStatusPaused {
			return asyncpkg.RunPausedError{RunID: result.RunID, Token: result.Token}
		}
		return nil
	}
	result, err := handler.framework.Run(ctx, RunRequest{
		RunID:   payload.RunID,
		Agent:   payload.Agent,
		Prompt:  payload.Prompt,
		Context: payload.Context,
	})
	if err != nil {
		return err
	}
	if result.Status == runstate.RunStatusPaused {
		return asyncpkg.RunPausedError{RunID: result.RunID, Token: result.Token}
	}
	return nil
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
		return asyncpkg.RunPausedError{RunID: result.RunID, Token: result.Token}
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
