package http

import (
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"time"

	asynchttp "github.com/aijustin/agentflow-go/internal/adapter/async/http"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type HandlerConfig struct {
	Queue             asyncpkg.Queue
	RunState          runstate.Repository
	Policy            security.Policy
	Audit             audit.Sink
	AuthMiddleware    func(nethttp.Handler) nethttp.Handler
	MetricsHandler    nethttp.Handler
	IDGenerator       func() string
	Now               func() time.Time
	MaxBodyBytes      int64
	Version           string
	EventsHandler     nethttp.Handler
	HITLHandler       nethttp.Handler
	CheckpointHandler nethttp.Handler
	StudioHandler     nethttp.Handler
	RetentionHandler  nethttp.Handler
}

type Handler struct {
	mux     *nethttp.ServeMux
	version string
}

func NewHandler(config HandlerConfig) (*Handler, error) {
	if config.Queue == nil {
		return nil, fmt.Errorf("api http: queue is nil")
	}
	runHandler, err := asynchttp.NewHandler(asynchttp.HandlerConfig{
		Queue:        config.Queue,
		RunState:     config.RunState,
		Policy:       config.Policy,
		Audit:        config.Audit,
		IDGenerator:  config.IDGenerator,
		Now:          config.Now,
		MaxBodyBytes: config.MaxBodyBytes,
	})
	if err != nil {
		return nil, err
	}
	jobHandler := nethttp.Handler(runHandler)
	runs := nethttp.Handler(&RunsMux{Checkpoint: config.CheckpointHandler, Async: jobHandler})
	if config.AuthMiddleware != nil {
		runs = config.AuthMiddleware(runs)
	}
	handler := &Handler{mux: nethttp.NewServeMux(), version: config.Version}
	handler.mux.HandleFunc("/healthz", handler.handleHealth)
	handler.mux.HandleFunc("/readyz", handler.handleHealth)
	handler.mux.Handle("/v1/runs", runs)
	handler.mux.Handle("/v1/runs/", runs)
	handler.mux.Handle("/v1/jobs", jobHandler)
	handler.mux.Handle("/v1/jobs/", jobHandler)
	if config.EventsHandler != nil {
		events := config.EventsHandler
		if config.AuthMiddleware != nil {
			events = config.AuthMiddleware(events)
		}
		handler.mux.Handle("/v1/events", events)
	}
	if config.HITLHandler != nil {
		hitl := config.HITLHandler
		if config.AuthMiddleware != nil {
			hitl = config.AuthMiddleware(hitl)
		}
		handler.mux.Handle("/v1/hitl/resume", hitl)
	}
	if config.StudioHandler != nil {
		studio := config.StudioHandler
		if config.AuthMiddleware != nil {
			studio = config.AuthMiddleware(studio)
		}
		handler.mux.Handle("/v1/studio/", studio)
	}
	if config.RetentionHandler != nil {
		retention := config.RetentionHandler
		if config.AuthMiddleware != nil {
			retention = config.AuthMiddleware(retention)
		}
		handler.mux.Handle("/v1/admin/retention/", retention)
	}
	if config.MetricsHandler != nil {
		handler.mux.Handle("/metrics", config.MetricsHandler)
	}
	return handler, nil
}

func (handler *Handler) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	handler.mux.ServeHTTP(w, r)
}

func (handler *Handler) handleHealth(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		w.Header().Set("Allow", nethttp.MethodGet)
		nethttp.Error(w, "method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]string{"status": "ok", "version": handler.version})
}

func writeJSON(w nethttp.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
