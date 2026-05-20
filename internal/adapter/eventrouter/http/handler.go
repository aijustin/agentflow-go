package http

import (
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"

	"github.com/aijustin/agentflow-go/pkg/eventrouter"
)

const DefaultMaxBodyBytes = int64(1 << 20)

type FrameworkRunner interface {
	HandleEvent(r *nethttp.Request, event eventrouter.Event) (any, error)
}

type Handler struct {
	runner       FrameworkRunner
	maxBodyBytes int64
}

type HandlerConfig struct {
	Framework    FrameworkRunner
	MaxBodyBytes int64
}

func NewHandler(config HandlerConfig) (*Handler, error) {
	if config.Framework == nil {
		return nil, fmt.Errorf("eventrouter http: framework is nil")
	}
	maxBodyBytes := config.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxBodyBytes
	}
	return &Handler{runner: config.Framework, maxBodyBytes: maxBodyBytes}, nil
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
	var event eventrouter.Event
	if err := json.Unmarshal(body, &event); err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	result, err := handler.runner.HandleEvent(r, event)
	if err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
