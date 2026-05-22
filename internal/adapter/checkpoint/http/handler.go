package http

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"
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

type HandlerConfig struct {
	Checkpoint   StepResumer
	Steps        StepsLister
	History      CheckpointLister
	Checkpoints  CheckpointLoader
	Restore      CheckpointResumer
	MaxBodyBytes int64
}

type Handler struct {
	checkpoint   StepResumer
	steps        StepsLister
	history      CheckpointLister
	checkpoints  CheckpointLoader
	restore      CheckpointResumer
	maxBodyBytes int64
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
		maxBodyBytes: maxBodyBytes,
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
	result, err := h.steps.ListRunSteps(r.Context(), runID)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err.Error())
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
	result, err := h.checkpoint.ResumeFromStep(r.Context(), runID, req.NodeID)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err.Error())
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
		writeError(w, nethttp.StatusBadRequest, err.Error())
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
	version, err := strconv.ParseInt(strings.TrimSpace(versionRaw), 10, 64)
	if err != nil || version <= 0 {
		writeError(w, nethttp.StatusBadRequest, "version must be a positive integer")
		return
	}
	result, err := h.checkpoints.GetRunCheckpoint(r.Context(), runID, version)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err.Error())
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
	result, err := h.restore.ResumeFromCheckpoint(r.Context(), runID, req.Version)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err.Error())
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
