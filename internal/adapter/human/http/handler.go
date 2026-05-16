package http

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type HumanGate interface {
	Resume(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) error
}

type Handler struct {
	gate HumanGate
}

func NewHandler(gate HumanGate) *Handler {
	return &Handler{gate: gate}
}

type resumeRequest struct {
	Token     string          `json:"token"`
	Decision  core.Decision   `json:"decision"`
	Amendment json.RawMessage `json:"amendment,omitempty"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req resumeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Token == "" || !req.Decision.Valid() {
		http.Error(w, "token and valid decision are required", http.StatusBadRequest)
		return
	}
	if err := h.gate.Resume(r.Context(), req.Token, req.Decision, req.Amendment); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
