package http

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"

	"github.com/aijustin/agentflow-go/pkg/core"
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
			nethttp.Error(w, err.Error(), nethttp.StatusConflict)
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
		nethttp.Error(w, err.Error(), nethttp.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
