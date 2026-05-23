package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

const DefaultMaxBodyBytes = int64(1 << 20)

type Purger interface {
	PurgeRuns(ctx context.Context, filter runstate.ListFilter) (int, error)
	PurgeExpired(ctx context.Context, maxAge time.Duration) (int, error)
	PurgeWithPolicy(ctx context.Context, policy RetentionPolicy) (int, error)
	PurgeOrphanBlobs(ctx context.Context) (int, error)
}

type RetentionPolicy struct {
	MaxAge       time.Duration
	Status       runstate.RunStatus
	ScenarioName string
	Limit        int
}

type HandlerConfig struct {
	Purger       Purger
	Policy       security.Policy
	Audit        audit.Sink
	MaxBodyBytes int64
}

type Handler struct {
	purger       Purger
	policy       security.Policy
	audit        audit.Sink
	maxBodyBytes int64
}

type purgeResponse struct {
	Removed int `json:"removed"`
}

type purgeRunsRequest struct {
	Status       runstate.RunStatus `json:"status,omitempty"`
	ScenarioName string             `json:"scenario_name,omitempty"`
	TenantID     string             `json:"tenant_id,omitempty"`
	Limit        int                `json:"limit,omitempty"`
}

type purgeExpiredRequest struct {
	MaxAge string `json:"max_age"`
}

type purgePolicyRequest struct {
	MaxAge       string             `json:"max_age,omitempty"`
	Status       runstate.RunStatus `json:"status,omitempty"`
	ScenarioName string             `json:"scenario_name,omitempty"`
	Limit        int                `json:"limit,omitempty"`
}

func NewHandler(config HandlerConfig) (*Handler, error) {
	if config.Purger == nil {
		return nil, errors.New("retention http: purger is nil")
	}
	maxBodyBytes := config.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxBodyBytes
	}
	return &Handler{
		purger:       config.Purger,
		policy:       config.Policy,
		audit:        config.Audit,
		maxBodyBytes: maxBodyBytes,
	}, nil
}

func (h *Handler) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/admin/retention"), "/")
	switch {
	case path == "purge-runs" && r.Method == nethttp.MethodPost:
		h.handlePurgeRuns(w, r)
	case path == "purge-expired" && r.Method == nethttp.MethodPost:
		h.handlePurgeExpired(w, r)
	case path == "purge-policy" && r.Method == nethttp.MethodPost:
		h.handlePurgePolicy(w, r)
	case path == "purge-blobs" && r.Method == nethttp.MethodPost:
		h.handlePurgeBlobs(w, r)
	default:
		nethttp.NotFound(w, r)
	}
}

func (h *Handler) handlePurgeRuns(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req purgeRunsRequest
	if err := decodeJSON(w, r, h.maxBodyBytes, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	resource := security.Resource{Type: "retention", ID: "purge-runs"}
	principal, ok := h.authorize(w, r, security.ActionAdminConfig, resource)
	if !ok {
		return
	}
	filter := runstate.ListFilter{
		Status:       req.Status,
		ScenarioName: req.ScenarioName,
		TenantID:     req.TenantID,
		Limit:        req.Limit,
	}
	if filter.TenantID == "" && principal.Scope.TenantID != "" {
		filter.TenantID = principal.Scope.TenantID
	}
	removed, err := h.purger.PurgeRuns(r.Context(), filter)
	if err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusInternalServerError)
		return
	}
	h.recordAudit(r.Context(), audit.Event{Type: audit.EventRunSubmitted, Principal: principal, Action: security.ActionAdminConfig, Resource: resource, Outcome: "purged_runs"})
	writeJSON(w, nethttp.StatusOK, purgeResponse{Removed: removed})
}

func (h *Handler) handlePurgeExpired(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req purgeExpiredRequest
	if err := decodeJSON(w, r, h.maxBodyBytes, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	maxAge, err := time.ParseDuration(strings.TrimSpace(req.MaxAge))
	if err != nil || maxAge <= 0 {
		nethttp.Error(w, "max_age duration is required", nethttp.StatusBadRequest)
		return
	}
	resource := security.Resource{Type: "retention", ID: "purge-expired"}
	principal, ok := h.authorize(w, r, security.ActionAdminConfig, resource)
	if !ok {
		return
	}
	removed, err := h.purger.PurgeExpired(r.Context(), maxAge)
	if err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusInternalServerError)
		return
	}
	h.recordAudit(r.Context(), audit.Event{Type: audit.EventRunSubmitted, Principal: principal, Action: security.ActionAdminConfig, Resource: resource, Outcome: "purged_expired"})
	writeJSON(w, nethttp.StatusOK, purgeResponse{Removed: removed})
}

func (h *Handler) handlePurgePolicy(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req purgePolicyRequest
	if err := decodeJSON(w, r, h.maxBodyBytes, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	policy := RetentionPolicy{Status: req.Status, ScenarioName: req.ScenarioName, Limit: req.Limit}
	if strings.TrimSpace(req.MaxAge) != "" {
		maxAge, err := time.ParseDuration(req.MaxAge)
		if err != nil || maxAge <= 0 {
			nethttp.Error(w, "max_age must be a positive duration", nethttp.StatusBadRequest)
			return
		}
		policy.MaxAge = maxAge
	}
	resource := security.Resource{Type: "retention", ID: "purge-policy"}
	principal, ok := h.authorize(w, r, security.ActionAdminConfig, resource)
	if !ok {
		return
	}
	removed, err := h.purger.PurgeWithPolicy(r.Context(), policy)
	if err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusInternalServerError)
		return
	}
	h.recordAudit(r.Context(), audit.Event{Type: audit.EventRunSubmitted, Principal: principal, Action: security.ActionAdminConfig, Resource: resource, Outcome: "purged_policy"})
	writeJSON(w, nethttp.StatusOK, purgeResponse{Removed: removed})
}

func (h *Handler) handlePurgeBlobs(w nethttp.ResponseWriter, r *nethttp.Request) {
	resource := security.Resource{Type: "retention", ID: "purge-blobs"}
	principal, ok := h.authorize(w, r, security.ActionAdminConfig, resource)
	if !ok {
		return
	}
	removed, err := h.purger.PurgeOrphanBlobs(r.Context())
	if err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusInternalServerError)
		return
	}
	h.recordAudit(r.Context(), audit.Event{Type: audit.EventRunSubmitted, Principal: principal, Action: security.ActionAdminConfig, Resource: resource, Outcome: "purged_blobs"})
	writeJSON(w, nethttp.StatusOK, purgeResponse{Removed: removed})
}

func (h *Handler) authorize(w nethttp.ResponseWriter, r *nethttp.Request, action security.Action, resource security.Resource) (identity.Principal, bool) {
	if h.policy == nil {
		return identity.Principal{}, true
	}
	principal, err := identity.RequirePrincipal(r.Context())
	if err != nil {
		nethttp.Error(w, "unauthorized", nethttp.StatusUnauthorized)
		return identity.Principal{}, false
	}
	resource = security.BindTenant(principal, resource)
	if err := h.policy.Authorize(r.Context(), principal, action, resource); err != nil {
		status := nethttp.StatusForbidden
		if errors.Is(err, security.ErrUnauthenticated) {
			status = nethttp.StatusUnauthorized
		}
		nethttp.Error(w, "forbidden", status)
		return identity.Principal{}, false
	}
	return principal, true
}

func (h *Handler) recordAudit(ctx context.Context, event audit.Event) {
	if h.audit == nil {
		return
	}
	_ = h.audit.Record(ctx, event.WithDefaults(time.Now().UTC()))
}

func decodeJSON(w nethttp.ResponseWriter, r *nethttp.Request, maxBodyBytes int64, out any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(nethttp.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain a single JSON object")
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

func writeJSON(w nethttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
