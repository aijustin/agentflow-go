package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"

	appexec "github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

const DefaultMaxBodyBytes = int64(1 << 20)

type Gate interface {
	Resume(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) error
}

type Continuer interface {
	ResumeAndContinue(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) (any, error)
}

type HandlerConfig struct {
	Gate         Gate
	Continuer    Continuer
	MaxBodyBytes int64
}

type Handler struct {
	gate         Gate
	continuer    Continuer
	maxBodyBytes int64
}

func NewHandler(config HandlerConfig) *Handler {
	maxBodyBytes := config.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxBodyBytes
	}
	return &Handler{gate: config.Gate, continuer: config.Continuer, maxBodyBytes: maxBodyBytes}
}

type resumeRequest struct {
	Token     string          `json:"token"`
	Decision  core.Decision   `json:"decision"`
	Amendment json.RawMessage `json:"amendment,omitempty"`
	Continue  bool            `json:"continue,omitempty"`
}

func (h *Handler) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		w.Header().Set("Allow", nethttp.MethodPost)
		nethttp.Error(w, "method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, h.maxBodyBytes+1))
	if err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	if int64(len(body)) > h.maxBodyBytes {
		nethttp.Error(w, "request body too large", nethttp.StatusRequestEntityTooLarge)
		return
	}
	var req resumeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	if req.Token == "" || !req.Decision.Valid() {
		nethttp.Error(w, "token and valid decision are required", nethttp.StatusBadRequest)
		return
	}
	if req.Continue {
		if h.continuer == nil {
			nethttp.Error(w, "continue is not configured", nethttp.StatusBadRequest)
			return
		}
		result, err := h.continuer.ResumeAndContinue(r.Context(), req.Token, req.Decision, req.Amendment)
		if err != nil {
			writeResumeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	if h.gate == nil {
		nethttp.Error(w, "human gate is not configured", nethttp.StatusServiceUnavailable)
		return
	}
	if err := h.gate.Resume(r.Context(), req.Token, req.Decision, req.Amendment); err != nil {
		writeResumeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// errorResponse is the structured error body for resume failures. ErrorCode
// is a stable machine-readable classifier so clients can branch without
// parsing the human-readable message.
type errorResponse struct {
	Error     string `json:"error"`
	ErrorCode string `json:"error_code"`
}

// classifyResumeError maps the classified resume/run sentinels onto HTTP
// status codes: expired credentials 410, malformed/invalid credentials 401,
// every lost race (superseded token, resume or run already in flight) 409,
// and anything else 500 so unexpected failures are not misreported as client
// conflicts. ErrTokenExpired wraps ErrInvalidToken, so it is checked first.
func classifyResumeError(err error) (int, string) {
	switch {
	case errors.Is(err, runstate.ErrTokenExpired):
		return nethttp.StatusGone, "token_expired"
	case errors.Is(err, runstate.ErrInvalidToken):
		return nethttp.StatusUnauthorized, "invalid_token"
	case errors.Is(err, runstate.ErrTokenSuperseded):
		return nethttp.StatusConflict, "token_superseded"
	case errors.Is(err, runstate.ErrResumeInProgress):
		return nethttp.StatusConflict, "resume_in_progress"
	case errors.Is(err, appexec.ErrRunInProgress):
		return nethttp.StatusConflict, "run_in_progress"
	default:
		return nethttp.StatusInternalServerError, "internal_error"
	}
}

func writeResumeError(w nethttp.ResponseWriter, err error) {
	status, code := classifyResumeError(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: err.Error(), ErrorCode: code})
}
