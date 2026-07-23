package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"

	appexec "github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/pkg/eventrouter"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

const DefaultMaxBodyBytes = int64(1 << 20)

type FrameworkRunner interface {
	HandleEvent(r *nethttp.Request, event eventrouter.Event) (any, error)
}

// SignatureVerifier validates the raw webhook request body before it is
// processed (e.g. HMAC signature headers from the event source). A nil
// verifier skips validation.
type SignatureVerifier func(r *nethttp.Request, body []byte) error

type Handler struct {
	runner       FrameworkRunner
	maxBodyBytes int64
	verifier     SignatureVerifier
}

type HandlerConfig struct {
	Framework    FrameworkRunner
	MaxBodyBytes int64
	// VerifySignature, when set, is called with the raw body before event
	// decoding; a non-nil error rejects the request with 401. Webhooks are
	// unauthenticated ingress by nature — production deployments should set
	// this or wrap the handler in an authenticating middleware.
	VerifySignature SignatureVerifier
}

func NewHandler(config HandlerConfig) (*Handler, error) {
	if config.Framework == nil {
		return nil, fmt.Errorf("eventrouter http: framework is nil")
	}
	maxBodyBytes := config.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxBodyBytes
	}
	return &Handler{runner: config.Framework, maxBodyBytes: maxBodyBytes, verifier: config.VerifySignature}, nil
}

func (handler *Handler) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		w.Header().Set("Allow", nethttp.MethodPost)
		nethttp.Error(w, "method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, handler.maxBodyBytes+1))
	if err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	if int64(len(body)) > handler.maxBodyBytes {
		nethttp.Error(w, "request body too large", nethttp.StatusRequestEntityTooLarge)
		return
	}
	if handler.verifier != nil {
		if err := handler.verifier(r, body); err != nil {
			writeError(w, nethttp.StatusUnauthorized, "invalid_signature", err.Error())
			return
		}
	}
	var event eventrouter.Event
	if err := json.Unmarshal(body, &event); err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	result, err := handler.runner.HandleEvent(r, event)
	if err != nil {
		status, code := classifyEventError(err)
		writeError(w, status, code, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// classifyEventError maps event-handling failures onto HTTP status codes:
// malformed events 400, unroutable events (no matching trigger) 404, runs
// that conflict with in-flight or finished executions 409, and anything else
// 500 so unexpected failures are not misreported as client errors.
func classifyEventError(err error) (int, string) {
	switch {
	case errors.Is(err, eventrouter.ErrEventTypeRequired):
		return nethttp.StatusBadRequest, "invalid_event"
	case errors.Is(err, eventrouter.ErrNoTrigger):
		return nethttp.StatusNotFound, "no_trigger"
	case errors.Is(err, appexec.ErrRunInProgress):
		return nethttp.StatusConflict, "run_in_progress"
	case errors.Is(err, appexec.ErrRunAlreadyCompleted):
		return nethttp.StatusConflict, "run_already_completed"
	case errors.Is(err, runstate.ErrNotFound):
		return nethttp.StatusNotFound, "not_found"
	default:
		return nethttp.StatusInternalServerError, "internal_error"
	}
}

func writeError(w nethttp.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message, "error_code": code})
}
