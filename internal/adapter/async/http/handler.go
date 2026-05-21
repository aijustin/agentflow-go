package http

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"time"

	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

const DefaultMaxBodyBytes = int64(1 << 20)

type HandlerConfig struct {
	Queue        asyncpkg.Queue
	Policy       security.Policy
	Audit        audit.Sink
	IDGenerator  func() string
	Now          func() time.Time
	MaxBodyBytes int64
}

type Handler struct {
	queue        asyncpkg.Queue
	policy       security.Policy
	audit        audit.Sink
	idGenerator  func() string
	now          func() time.Time
	maxBodyBytes int64
}

type SubmitRunRequest struct {
	RunID       string            `json:"run_id,omitempty"`
	ScenarioID  string            `json:"scenario_id,omitempty"`
	Scenario    json.RawMessage   `json:"scenario,omitempty"`
	Agent       string            `json:"agent,omitempty"`
	Prompt      string            `json:"prompt,omitempty"`
	Context     json.RawMessage   `json:"context,omitempty"`
	MaxAttempts int               `json:"max_attempts,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type SubmitEventRequest struct {
	Type        string            `json:"type"`
	RunID       string            `json:"run_id,omitempty"`
	Payload     json.RawMessage   `json:"payload,omitempty"`
	JobID       string            `json:"job_id,omitempty"`
	MaxAttempts int               `json:"max_attempts,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type SubmitResumeContinueRequest struct {
	Token       string            `json:"token"`
	Decision    string            `json:"decision"`
	Amendment   json.RawMessage   `json:"amendment,omitempty"`
	JobID       string            `json:"job_id,omitempty"`
	RunID       string            `json:"run_id,omitempty"`
	MaxAttempts int               `json:"max_attempts,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type JobResponse struct {
	Job asyncpkg.Job `json:"job"`
}

type JobsResponse struct {
	Jobs []asyncpkg.Job `json:"jobs"`
}

func NewHandler(config HandlerConfig) (*Handler, error) {
	if config.Queue == nil {
		return nil, fmt.Errorf("async http: queue is nil")
	}
	idGenerator := config.IDGenerator
	if idGenerator == nil {
		idGenerator = generateRunID
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	maxBodyBytes := config.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxBodyBytes
	}
	return &Handler{queue: config.Queue, policy: config.Policy, audit: config.Audit, idGenerator: idGenerator, now: now, maxBodyBytes: maxBodyBytes}, nil
}

func (handler *Handler) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	path := strings.Trim(r.URL.Path, "/")
	switch path {
	case "v1/runs":
		if r.Method != nethttp.MethodPost {
			w.Header().Set("Allow", nethttp.MethodPost)
			nethttp.Error(w, "method not allowed", nethttp.StatusMethodNotAllowed)
			return
		}
		handler.handleSubmitRun(w, r)
		return
	case "v1/jobs/events":
		if r.Method != nethttp.MethodPost {
			w.Header().Set("Allow", nethttp.MethodPost)
			nethttp.Error(w, "method not allowed", nethttp.StatusMethodNotAllowed)
			return
		}
		handler.handleSubmitEvent(w, r)
		return
	case "v1/jobs/hitl/resume":
		if r.Method != nethttp.MethodPost {
			w.Header().Set("Allow", nethttp.MethodPost)
			nethttp.Error(w, "method not allowed", nethttp.StatusMethodNotAllowed)
			return
		}
		handler.handleSubmitResumeContinue(w, r)
		return
	case "v1/jobs":
		if r.Method != nethttp.MethodGet {
			w.Header().Set("Allow", nethttp.MethodGet)
			nethttp.Error(w, "method not allowed", nethttp.StatusMethodNotAllowed)
			return
		}
		handler.handleListJobs(w, r)
		return
	}
	if strings.HasPrefix(path, "v1/runs/") {
		parts := strings.Split(path, "/")
		if len(parts) == 3 && r.Method == nethttp.MethodGet {
			handler.handleRead(w, r, parts[2])
			return
		}
		if len(parts) == 4 && parts[3] == "cancel" && r.Method == nethttp.MethodPost {
			handler.handleCancel(w, r, parts[2])
			return
		}
	}
	if strings.HasPrefix(path, "v1/jobs/") {
		parts := strings.Split(path, "/")
		if len(parts) == 3 && r.Method == nethttp.MethodGet {
			handler.handleRead(w, r, parts[2])
			return
		}
		if len(parts) == 4 && parts[3] == "cancel" && r.Method == nethttp.MethodPost {
			handler.handleCancel(w, r, parts[2])
			return
		}
		if len(parts) == 4 && parts[3] == "requeue" && r.Method == nethttp.MethodPost {
			handler.handleRequeue(w, r, parts[2])
			return
		}
	}
	nethttp.NotFound(w, r)
}

func (handler *Handler) handleSubmitRun(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req SubmitRunRequest
	if err := decodeJSON(w, r, handler.maxBodyBytes, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		runID = strings.TrimSpace(handler.idGenerator())
	}
	if runID == "" {
		nethttp.Error(w, "run_id is required", nethttp.StatusBadRequest)
		return
	}
	resource := security.Resource{Type: "run", ID: runID}
	principal, ok := handler.authorize(w, r, security.ActionRunSubmit, resource)
	if !ok {
		return
	}
	req.RunID = runID
	payload, err := json.Marshal(asyncpkg.RunPayload{
		RunID:       req.RunID,
		ScenarioID:  req.ScenarioID,
		Scenario:    req.Scenario,
		Agent:       req.Agent,
		Prompt:      req.Prompt,
		Context:     req.Context,
		MaxAttempts: req.MaxAttempts,
		Metadata:    req.Metadata,
		Principal:   principal,
	})
	if err != nil {
		nethttp.Error(w, "encode job payload", nethttp.StatusInternalServerError)
		return
	}
	now := handler.now().UTC()
	job, err := handler.queue.Enqueue(r.Context(), asyncpkg.Job{ID: runID, Type: asyncpkg.RunJobType, RunID: runID, Payload: payload, MaxAttempts: req.MaxAttempts, CreatedAt: now, UpdatedAt: now, AvailableAt: now})
	if err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusConflict)
		return
	}
	handler.recordAudit(r.Context(), audit.Event{Type: audit.EventRunSubmitted, Principal: principal, Action: security.ActionRunSubmit, Resource: resource, RunID: runID, Outcome: string(job.State)})
	writeJSON(w, nethttp.StatusAccepted, JobResponse{Job: job})
}

func (handler *Handler) handleSubmitEvent(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req SubmitEventRequest
	if err := decodeJSON(w, r, handler.maxBodyBytes, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	if strings.TrimSpace(req.Type) == "" {
		nethttp.Error(w, "type is required", nethttp.StatusBadRequest)
		return
	}
	jobID := strings.TrimSpace(req.JobID)
	if jobID == "" {
		jobID = strings.TrimSpace(req.RunID)
	}
	if jobID == "" {
		jobID = strings.TrimSpace(handler.idGenerator())
	}
	resource := security.Resource{Type: "event", ID: jobID}
	principal, ok := handler.authorize(w, r, security.ActionRunSubmit, resource)
	if !ok {
		return
	}
	payload, err := json.Marshal(asyncpkg.EventPayload{
		Type:        req.Type,
		RunID:       req.RunID,
		Payload:     req.Payload,
		MaxAttempts: req.MaxAttempts,
		Metadata:    req.Metadata,
		Principal:   principal,
	})
	if err != nil {
		nethttp.Error(w, "encode job payload", nethttp.StatusInternalServerError)
		return
	}
	now := handler.now().UTC()
	job, err := handler.queue.Enqueue(r.Context(), asyncpkg.Job{
		ID:          jobID,
		Type:        asyncpkg.EventJobType,
		RunID:       req.RunID,
		Payload:     payload,
		MaxAttempts: req.MaxAttempts,
		CreatedAt:   now,
		UpdatedAt:   now,
		AvailableAt: now,
	})
	if err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusConflict)
		return
	}
	handler.recordAudit(r.Context(), audit.Event{Type: audit.EventRunSubmitted, Principal: principal, Action: security.ActionRunSubmit, Resource: resource, RunID: req.RunID, Outcome: string(job.State)})
	writeJSON(w, nethttp.StatusAccepted, JobResponse{Job: job})
}

func (handler *Handler) handleSubmitResumeContinue(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req SubmitResumeContinueRequest
	if err := decodeJSON(w, r, handler.maxBodyBytes, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	var decision core.Decision
	if err := decision.UnmarshalText([]byte(req.Decision)); err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	if req.Token == "" {
		nethttp.Error(w, "token is required", nethttp.StatusBadRequest)
		return
	}
	jobID := strings.TrimSpace(req.JobID)
	if jobID == "" {
		jobID = strings.TrimSpace(req.RunID)
	}
	if jobID == "" {
		jobID = strings.TrimSpace(handler.idGenerator())
	}
	resource := security.Resource{Type: "hitl", ID: jobID}
	principal, ok := handler.authorize(w, r, security.ActionHITLResume, resource)
	if !ok {
		return
	}
	payload, err := json.Marshal(asyncpkg.ResumeContinuePayload{
		Token:       req.Token,
		Decision:    decision,
		Amendment:   req.Amendment,
		RunID:       req.RunID,
		MaxAttempts: req.MaxAttempts,
		Metadata:    req.Metadata,
		Principal:   principal,
	})
	if err != nil {
		nethttp.Error(w, "encode job payload", nethttp.StatusInternalServerError)
		return
	}
	now := handler.now().UTC()
	job, err := handler.queue.Enqueue(r.Context(), asyncpkg.Job{
		ID:          jobID,
		Type:        asyncpkg.ResumeContinueJobType,
		RunID:       req.RunID,
		Payload:     payload,
		MaxAttempts: req.MaxAttempts,
		CreatedAt:   now,
		UpdatedAt:   now,
		AvailableAt: now,
	})
	if err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusConflict)
		return
	}
	handler.recordAudit(r.Context(), audit.Event{Type: audit.EventRunSubmitted, Principal: principal, Action: security.ActionHITLResume, Resource: resource, RunID: req.RunID, Outcome: string(job.State)})
	writeJSON(w, nethttp.StatusAccepted, JobResponse{Job: job})
}

func (handler *Handler) handleRead(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	resource := security.Resource{Type: "run", ID: runID}
	if _, ok := handler.authorize(w, r, security.ActionRunRead, resource); !ok {
		return
	}
	job, err := handler.queue.Load(r.Context(), runID)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, JobResponse{Job: job})
}

func (handler *Handler) handleListJobs(w nethttp.ResponseWriter, r *nethttp.Request) {
	admin, ok := handler.queue.(asyncpkg.JobAdmin)
	if !ok {
		nethttp.Error(w, "job listing is not supported", nethttp.StatusNotImplemented)
		return
	}
	resource := security.Resource{Type: "queue", ID: "jobs"}
	if _, ok := handler.authorize(w, r, security.ActionRunRead, resource); !ok {
		return
	}
	state := asyncpkg.JobState(strings.TrimSpace(r.URL.Query().Get("state")))
	filter := asyncpkg.JobFilter{State: state}
	jobs, err := admin.ListJobs(r.Context(), filter)
	if err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusInternalServerError)
		return
	}
	writeJSON(w, nethttp.StatusOK, JobsResponse{Jobs: jobs})
}

func (handler *Handler) handleRequeue(w nethttp.ResponseWriter, r *nethttp.Request, jobID string) {
	admin, ok := handler.queue.(asyncpkg.JobAdmin)
	if !ok {
		nethttp.Error(w, "job requeue is not supported", nethttp.StatusNotImplemented)
		return
	}
	resource := security.Resource{Type: "job", ID: jobID}
	principal, ok := handler.authorize(w, r, security.ActionRunSubmit, resource)
	if !ok {
		return
	}
	if err := admin.Requeue(r.Context(), jobID); err != nil {
		writeQueueError(w, err)
		return
	}
	job, err := handler.queue.Load(r.Context(), jobID)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	handler.recordAudit(r.Context(), audit.Event{Type: audit.EventRunSubmitted, Principal: principal, Action: security.ActionRunSubmit, Resource: resource, RunID: job.RunID, Outcome: string(job.State)})
	writeJSON(w, nethttp.StatusOK, JobResponse{Job: job})
}

func (handler *Handler) handleCancel(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	resource := security.Resource{Type: "run", ID: runID}
	principal, ok := handler.authorize(w, r, security.ActionRunCancel, resource)
	if !ok {
		return
	}
	if err := handler.queue.Cancel(r.Context(), runID); err != nil {
		writeQueueError(w, err)
		return
	}
	job, err := handler.queue.Load(r.Context(), runID)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	handler.recordAudit(r.Context(), audit.Event{Type: audit.EventRunCancelled, Principal: principal, Action: security.ActionRunCancel, Resource: resource, RunID: runID, Outcome: string(job.State)})
	writeJSON(w, nethttp.StatusOK, JobResponse{Job: job})
}

func (handler *Handler) authorize(w nethttp.ResponseWriter, r *nethttp.Request, action security.Action, resource security.Resource) (identity.Principal, bool) {
	if handler.policy == nil {
		return principalFromContext(r.Context()), true
	}
	principal, err := identity.RequirePrincipal(r.Context())
	if err != nil {
		handler.recordDenied(r.Context(), identity.Principal{}, action, resource, security.ErrUnauthenticated)
		nethttp.Error(w, "unauthorized", nethttp.StatusUnauthorized)
		return identity.Principal{}, false
	}
	resource = security.BindTenant(principal, resource)
	if err := handler.policy.Authorize(r.Context(), principal, action, resource); err != nil {
		handler.recordDenied(r.Context(), principal, action, resource, err)
		status := nethttp.StatusInternalServerError
		message := "authorization failed"
		switch {
		case errors.Is(err, security.ErrUnauthenticated):
			status = nethttp.StatusUnauthorized
			message = "unauthorized"
		case errors.Is(err, security.ErrUnauthorized):
			status = nethttp.StatusForbidden
			message = "forbidden"
		}
		nethttp.Error(w, message, status)
		return identity.Principal{}, false
	}
	return principal, true
}

func (handler *Handler) recordDenied(ctx context.Context, principal identity.Principal, action security.Action, resource security.Resource, reason error) {
	handler.recordAudit(ctx, audit.Event{Type: audit.EventPolicyDenied, Principal: principal, Action: action, Resource: resource, RunID: resource.ID, Outcome: "denied", Reason: reason.Error()})
}

func (handler *Handler) recordAudit(ctx context.Context, event audit.Event) {
	if handler.audit == nil {
		return
	}
	_ = handler.audit.Record(ctx, event.WithDefaults(handler.now().UTC()))
}

func decodeJSON(w nethttp.ResponseWriter, r *nethttp.Request, maxBodyBytes int64, out any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(nethttp.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("request body must contain a single JSON object")
	}
	return nil
}

func writeDecodeError(w nethttp.ResponseWriter, err error) {
	var maxBytesErr *nethttp.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		nethttp.Error(w, "request body too large", nethttp.StatusRequestEntityTooLarge)
		return
	}
	nethttp.Error(w, err.Error(), nethttp.StatusBadRequest)
}

func writeQueueError(w nethttp.ResponseWriter, err error) {
	if errors.Is(err, asyncpkg.ErrJobNotFound) {
		nethttp.Error(w, "run not found", nethttp.StatusNotFound)
		return
	}
	nethttp.Error(w, err.Error(), nethttp.StatusConflict)
}

func writeJSON(w nethttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func principalFromContext(ctx context.Context) identity.Principal {
	principal, _ := identity.PrincipalFromContext(ctx)
	return principal
}

// generateRunID returns a cryptographically random run identifier with a
// "run-" prefix.  Falls back to a nanosecond timestamp on the rare occasion
// that the random reader fails.
func generateRunID() string {
	var b [8]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return "run-" + hex.EncodeToString(b[:])
}
