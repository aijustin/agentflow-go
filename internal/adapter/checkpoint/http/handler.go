package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"

	appexec "github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

const DefaultMaxBodyBytes = int64(1 << 20)

type StepsLister interface {
	ListRunSteps(ctx context.Context, runID string) (any, error)
}

type StepResumer interface {
	ResumeFromStep(ctx context.Context, runID, nodeID string) (any, error)
}

type CheckpointLister interface {
	ListRunCheckpoints(ctx context.Context, runID string, limit int) (any, error)
}

type CheckpointLoader interface {
	GetRunCheckpoint(ctx context.Context, runID string, version int64) (any, error)
}

type CheckpointResumer interface {
	ResumeFromCheckpoint(ctx context.Context, runID string, version int64) (any, error)
}

type RunForker interface {
	ForkRun(ctx context.Context, runID string, version int64) (any, error)
}

type HandlerConfig struct {
	Checkpoint   StepResumer
	Steps        StepsLister
	History      CheckpointLister
	Checkpoints  CheckpointLoader
	Restore      CheckpointResumer
	Fork         RunForker
	MaxBodyBytes int64
	// Policy authorizes requests, mirroring the async run handler: reads are
	// authorized as run.read and writes (resume-from-step,
	// resume-from-checkpoint, fork) as hitl.resume / run.submit. When Policy
	// is nil, write endpoints default-deny with 403 auth_required unless
	// InsecureAllowNoAuth explicitly opts out.
	Policy security.Policy
	// Audit receives policy-denied records when configured.
	Audit audit.Sink
	// InsecureAllowNoAuth disables the default-deny protection on write
	// endpoints when no Policy is configured. Only set this behind an
	// authenticating reverse proxy or in tests.
	InsecureAllowNoAuth bool
}

type Handler struct {
	checkpoint   StepResumer
	steps        StepsLister
	history      CheckpointLister
	checkpoints  CheckpointLoader
	restore      CheckpointResumer
	fork         RunForker
	maxBodyBytes int64
	policy       security.Policy
	audit        audit.Sink
	insecure     bool
}

func NewHandler(config HandlerConfig) *Handler {
	maxBodyBytes := config.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxBodyBytes
	}
	return &Handler{
		checkpoint:   config.Checkpoint,
		steps:        config.Steps,
		history:      config.History,
		checkpoints:  config.Checkpoints,
		restore:      config.Restore,
		fork:         config.Fork,
		maxBodyBytes: maxBodyBytes,
		policy:       config.Policy,
		audit:        config.Audit,
		insecure:     config.InsecureAllowNoAuth,
	}
}

func (h *Handler) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	path := strings.Trim(r.URL.Path, "/")
	if !strings.HasPrefix(path, "v1/runs/") {
		nethttp.NotFound(w, r)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) < 4 || parts[0] != "v1" || parts[1] != "runs" || parts[2] == "" {
		nethttp.NotFound(w, r)
		return
	}
	runID := parts[2]
	switch {
	case len(parts) == 4 && parts[3] == "steps":
		h.handleSteps(w, r, runID)
	case len(parts) == 4 && parts[3] == "resume-from-step":
		h.handleResumeFromStep(w, r, runID)
	case len(parts) == 4 && parts[3] == "checkpoints":
		h.handleCheckpoints(w, r, runID)
	case len(parts) == 4 && parts[3] == "resume-from-checkpoint":
		h.handleResumeFromCheckpoint(w, r, runID)
	case len(parts) == 4 && parts[3] == "fork":
		h.handleFork(w, r, runID)
	case len(parts) == 5 && parts[3] == "checkpoints":
		h.handleCheckpointVersion(w, r, runID, parts[4])
	default:
		nethttp.NotFound(w, r)
	}
}

func (h *Handler) handleSteps(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	if r.Method != nethttp.MethodGet {
		methodNotAllowed(w, nethttp.MethodGet)
		return
	}
	if h.steps == nil {
		writeError(w, nethttp.StatusNotImplemented, "steps listing is not configured")
		return
	}
	if !h.requireReadAuth(w, r, runID) {
		return
	}
	result, err := h.steps.ListRunSteps(r.Context(), runID)
	if err != nil {
		writeClassifiedError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (h *Handler) handleResumeFromStep(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if h.checkpoint == nil {
		writeError(w, nethttp.StatusNotImplemented, "resume-from-step is not configured")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, h.maxBodyBytes+1))
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err.Error())
		return
	}
	if int64(len(body)) > h.maxBodyBytes {
		writeError(w, nethttp.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	var req struct {
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, nethttp.StatusBadRequest, err.Error())
		return
	}
	req.NodeID = strings.TrimSpace(req.NodeID)
	if req.NodeID == "" {
		writeError(w, nethttp.StatusBadRequest, "node_id is required")
		return
	}
	if !h.requireWriteAuth(w, r, security.ActionHITLResume, runID) {
		return
	}
	result, err := h.checkpoint.ResumeFromStep(r.Context(), runID, req.NodeID)
	if err != nil {
		writeClassifiedError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (h *Handler) handleCheckpoints(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	if r.Method != nethttp.MethodGet {
		methodNotAllowed(w, nethttp.MethodGet)
		return
	}
	if h.history == nil {
		writeError(w, nethttp.StatusNotImplemented, "checkpoint history is not configured")
		return
	}
	if !h.requireReadAuth(w, r, runID) {
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, nethttp.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		limit = parsed
	}
	result, err := h.history.ListRunCheckpoints(r.Context(), runID, limit)
	if err != nil {
		writeClassifiedError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (h *Handler) handleCheckpointVersion(w nethttp.ResponseWriter, r *nethttp.Request, runID, versionRaw string) {
	if r.Method != nethttp.MethodGet {
		methodNotAllowed(w, nethttp.MethodGet)
		return
	}
	if h.checkpoints == nil {
		writeError(w, nethttp.StatusNotImplemented, "checkpoint loading is not configured")
		return
	}
	if !h.requireReadAuth(w, r, runID) {
		return
	}
	version, err := strconv.ParseInt(strings.TrimSpace(versionRaw), 10, 64)
	if err != nil || version <= 0 {
		writeError(w, nethttp.StatusBadRequest, "version must be a positive integer")
		return
	}
	result, err := h.checkpoints.GetRunCheckpoint(r.Context(), runID, version)
	if err != nil {
		writeClassifiedError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (h *Handler) handleResumeFromCheckpoint(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if h.restore == nil {
		writeError(w, nethttp.StatusNotImplemented, "resume-from-checkpoint is not configured")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, h.maxBodyBytes+1))
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err.Error())
		return
	}
	if int64(len(body)) > h.maxBodyBytes {
		writeError(w, nethttp.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	var req struct {
		Version int64 `json:"version"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, nethttp.StatusBadRequest, err.Error())
		return
	}
	if req.Version <= 0 {
		writeError(w, nethttp.StatusBadRequest, "version must be a positive integer")
		return
	}
	if !h.requireWriteAuth(w, r, security.ActionHITLResume, runID) {
		return
	}
	result, err := h.restore.ResumeFromCheckpoint(r.Context(), runID, req.Version)
	if err != nil {
		writeClassifiedError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (h *Handler) handleFork(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if h.fork == nil {
		writeError(w, nethttp.StatusNotImplemented, "run fork is not configured")
		return
	}
	var req struct {
		Version int64 `json:"version"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, h.maxBodyBytes+1))
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err.Error())
		return
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, nethttp.StatusBadRequest, err.Error())
			return
		}
	}
	if !h.requireWriteAuth(w, r, security.ActionRunSubmit, runID) {
		return
	}
	result, err := h.fork.ForkRun(r.Context(), runID, req.Version)
	if err != nil {
		writeClassifiedError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func methodNotAllowed(w nethttp.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	nethttp.Error(w, "method not allowed", nethttp.StatusMethodNotAllowed)
}

func writeJSON(w nethttp.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w nethttp.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// classifyRunstateError maps run-state/checkpoint failures onto HTTP status
// codes: unknown runs or checkpoint versions 404, lost version races and
// invalid status transitions 409, anything else 500 so unexpected failures
// are not misreported as client errors.
func classifyRunstateError(err error) (int, string) {
	switch {
	case errors.Is(err, runstate.ErrNotFound):
		return nethttp.StatusNotFound, "not_found"
	case errors.Is(err, runstate.ErrStaleSnapshot):
		return nethttp.StatusConflict, "stale_snapshot"
	case errors.Is(err, runstate.ErrInvalidTransition):
		return nethttp.StatusConflict, "invalid_transition"
	case errors.Is(err, runstate.ErrResumeInProgress):
		return nethttp.StatusConflict, "resume_in_progress"
	case errors.Is(err, appexec.ErrRunInProgress):
		return nethttp.StatusConflict, "run_in_progress"
	case errors.Is(err, appexec.ErrRunAlreadyCompleted):
		return nethttp.StatusConflict, "run_already_completed"
	default:
		return nethttp.StatusInternalServerError, "internal_error"
	}
}

// writeClassifiedError writes an adapter-call failure with a structured
// error_code derived from classifyRunstateError.
func writeClassifiedError(w nethttp.ResponseWriter, err error) {
	status, code := classifyRunstateError(err)
	writeJSON(w, status, map[string]string{"error": err.Error(), "error_code": code})
}

// requireWriteAuth enforces the write-endpoint authorization contract: with
// a Policy configured the caller must pass it (mirroring the async run
// handler); without one, writes default-deny unless InsecureAllowNoAuth was
// set explicitly. Mounting resume/fork endpoints without authorization is
// exactly the exposure this guard exists to prevent.
func (h *Handler) requireWriteAuth(w nethttp.ResponseWriter, r *nethttp.Request, action security.Action, runID string) bool {
	if h.policy == nil {
		if h.insecure {
			return true
		}
		writeJSON(w, nethttp.StatusForbidden, map[string]string{
			"error":      "checkpoint write endpoints require an authorization policy; configure Policy or explicitly set InsecureAllowNoAuth to disable this protection",
			"error_code": "auth_required",
		})
		return false
	}
	return h.authorize(w, r, action, security.Resource{Type: "run", ID: runID})
}

// requireReadAuth protects checkpoint payloads with the same default-deny
// contract as writes. Step outputs and snapshots may contain prompts, tool
// results, or other tenant data and must not become IDOR endpoints.
func (h *Handler) requireReadAuth(w nethttp.ResponseWriter, r *nethttp.Request, runID string) bool {
	if h.policy == nil {
		if h.insecure {
			return true
		}
		writeJSON(w, nethttp.StatusForbidden, map[string]string{
			"error":      "checkpoint read endpoints require an authorization policy; configure Policy or explicitly set InsecureAllowNoAuth to disable this protection",
			"error_code": "auth_required",
		})
		return false
	}
	return h.authorize(w, r, security.ActionRunRead, security.Resource{Type: "run", ID: runID})
}

func (h *Handler) authorize(w nethttp.ResponseWriter, r *nethttp.Request, action security.Action, resource security.Resource) bool {
	principal, err := identity.RequirePrincipal(r.Context())
	if err != nil {
		h.recordDenied(r, principal, action, resource, security.ErrUnauthenticated)
		writeJSON(w, nethttp.StatusUnauthorized, map[string]string{"error": "unauthorized", "error_code": "unauthenticated"})
		return false
	}
	resource = security.BindTenant(principal, resource)
	if err := h.policy.Authorize(r.Context(), principal, action, resource); err != nil {
		h.recordDenied(r, principal, action, resource, err)
		status := nethttp.StatusForbidden
		code := "forbidden"
		if errors.Is(err, security.ErrUnauthenticated) {
			status = nethttp.StatusUnauthorized
			code = "unauthenticated"
		}
		writeJSON(w, status, map[string]string{"error": "forbidden", "error_code": code})
		return false
	}
	return true
}

func (h *Handler) recordDenied(r *nethttp.Request, principal identity.Principal, action security.Action, resource security.Resource, reason error) {
	if h.audit == nil {
		return
	}
	_ = h.audit.Record(r.Context(), audit.Event{Type: audit.EventPolicyDenied, Principal: principal, Action: action, Resource: resource, RunID: resource.ID, Outcome: "denied", Reason: reason.Error()})
}
