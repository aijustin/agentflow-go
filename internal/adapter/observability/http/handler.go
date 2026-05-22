package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"strconv"
	"strings"

	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
)

type Config struct {
	Store          obspkg.EventStore
	Hub            *obspkg.EventHub
	AuthMiddleware func(nethttp.Handler) nethttp.Handler
	Steps          StepsLister
	Graph          GraphExporter
	Resume         StepResumer
}

type StepsLister interface {
	ListRunSteps(ctx context.Context, runID string) (any, error)
}

type StepResumer interface {
	ResumeFromStep(ctx context.Context, runID, nodeID string) (any, error)
}

type GraphExporter interface {
	ExportScenarioGraph() any
}

type Handler struct {
	store   obspkg.EventStore
	hub     *obspkg.EventHub
	steps   StepsLister
	graph   GraphExporter
	resume  StepResumer
	mux     *nethttp.ServeMux
	handler nethttp.Handler
}

func NewHandler(config Config) (*Handler, error) {
	if config.Store == nil {
		return nil, fmt.Errorf("observability http: event store is nil")
	}
	handler := &Handler{
		store:  config.Store,
		hub:    config.Hub,
		steps:  config.Steps,
		graph:  config.Graph,
		resume: config.Resume,
		mux:    nethttp.NewServeMux(),
	}
	handler.routes()
	handler.handler = handler.mux
	if config.AuthMiddleware != nil {
		handler.handler = config.AuthMiddleware(handler.mux)
	}
	return handler, nil
}

func (handler *Handler) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	handler.handler.ServeHTTP(w, r)
}

func (handler *Handler) routes() {
	handler.mux.HandleFunc("/", handler.handleDashboard)
	handler.mux.HandleFunc("/api/graph", handler.handleGraph)
	handler.mux.HandleFunc("/api/runs", handler.handleRuns)
	handler.mux.HandleFunc("/api/runs/", handler.handleRunResource)
}

func (handler *Handler) handleGraph(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		methodNotAllowed(w, nethttp.MethodGet)
		return
	}
	if handler.graph == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("scenario graph export is not configured"))
		return
	}
	writeJSON(w, nethttp.StatusOK, handler.graph.ExportScenarioGraph())
}

func (handler *Handler) handleDashboard(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.URL.Path != "/" {
		nethttp.NotFound(w, r)
		return
	}
	if r.Method != nethttp.MethodGet {
		methodNotAllowed(w, nethttp.MethodGet)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(nethttp.StatusOK)
	_, _ = w.Write([]byte(indexHTML))
}

func (handler *Handler) handleRuns(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		methodNotAllowed(w, nethttp.MethodGet)
		return
	}
	query := obspkg.RunQuery{
		Status: obspkg.RunStatus(r.URL.Query().Get("status")),
		Limit:  parseInt(r.URL.Query().Get("limit"), obspkg.DefaultRunQueryLimit),
		Offset: parseInt(r.URL.Query().Get("offset"), 0),
	}
	runs, err := handler.store.ListRuns(r.Context(), query)
	if err != nil {
		writeError(w, nethttp.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"runs": runs})
}

func (handler *Handler) handleRunResource(w nethttp.ResponseWriter, r *nethttp.Request) {
	runID, action, ok := splitRunResource(r.URL.Path)
	if !ok {
		nethttp.NotFound(w, r)
		return
	}
	switch action {
	case "events":
		handler.handleEvents(w, r, runID)
	case "stream":
		handler.handleStream(w, r, runID)
	case "steps":
		handler.handleSteps(w, r, runID)
	case "resume-from-step":
		handler.handleResumeFromStep(w, r, runID)
	default:
		nethttp.NotFound(w, r)
	}
}

func (handler *Handler) handleSteps(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	if r.Method != nethttp.MethodGet {
		methodNotAllowed(w, nethttp.MethodGet)
		return
	}
	if handler.steps == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("run steps listing is not configured"))
		return
	}
	result, err := handler.steps.ListRunSteps(r.Context(), runID)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (handler *Handler) handleResumeFromStep(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if handler.resume == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("resume-from-step is not configured"))
		return
	}
	var body struct {
		NodeID string `json:"node_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, nethttp.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	body.NodeID = strings.TrimSpace(body.NodeID)
	if body.NodeID == "" {
		writeError(w, nethttp.StatusBadRequest, fmt.Errorf("node_id is required"))
		return
	}
	result, err := handler.resume.ResumeFromStep(r.Context(), runID, body.NodeID)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (handler *Handler) handleEvents(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	if r.Method != nethttp.MethodGet {
		methodNotAllowed(w, nethttp.MethodGet)
		return
	}
	events, err := handler.store.ListEvents(r.Context(), runID, eventQueryFromURL(r.URL.Query()))
	if err != nil {
		writeError(w, nethttp.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"events": events})
}

func (handler *Handler) handleStream(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	if r.Method != nethttp.MethodGet {
		methodNotAllowed(w, nethttp.MethodGet)
		return
	}
	flusher, ok := w.(nethttp.Flusher)
	if !ok {
		writeError(w, nethttp.StatusInternalServerError, fmt.Errorf("streaming is not supported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(nethttp.StatusOK)
	_, _ = w.Write([]byte("retry: 2000\n\n"))
	flusher.Flush()
	lastSequence := eventQueryFromURL(r.URL.Query()).AfterSequence
	if handler.hub != nil {
		subscription := handler.hub.Subscribe(r.Context(), obspkg.EventSubscriptionFilter{RunID: runID, Buffer: 128})
		defer subscription.Cancel()
		if !handler.writeBacklog(w, flusher, r, runID, &lastSequence) {
			return
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case record, ok := <-subscription.Events:
				if !ok {
					return
				}
				if record.Sequence <= lastSequence {
					continue
				}
				for record.Sequence > lastSequence+1 {
					previous := lastSequence
					if !handler.writeBacklog(w, flusher, r, runID, &lastSequence) {
						return
					}
					if lastSequence == previous {
						break
					}
				}
				if record.Sequence <= lastSequence {
					continue
				}
				if !writeSSE(w, flusher, record) {
					return
				}
				lastSequence = record.Sequence
			}
		}
	}
	_ = handler.writeBacklog(w, flusher, r, runID, &lastSequence)
}

func (handler *Handler) writeBacklog(w nethttp.ResponseWriter, flusher nethttp.Flusher, r *nethttp.Request, runID string, lastSequence *int64) bool {
	events, err := handler.store.ListEvents(r.Context(), runID, obspkg.EventQuery{AfterSequence: *lastSequence, Limit: obspkg.MaxEventQueryLimit})
	if err != nil {
		writeSSEError(w, flusher, err)
		return false
	}
	for _, record := range events {
		if !writeSSE(w, flusher, record) {
			return false
		}
		*lastSequence = record.Sequence
	}
	return true
}

func splitRunResource(path string) (string, string, bool) {
	suffix := strings.TrimPrefix(path, "/api/runs/")
	parts := strings.Split(strings.Trim(suffix, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	runID, err := url.PathUnescape(parts[0])
	if err != nil || runID == "" {
		return "", "", false
	}
	return runID, parts[1], true
}

func eventQueryFromURL(values url.Values) obspkg.EventQuery {
	return obspkg.EventQuery{
		AfterSequence: int64(parseInt(values.Get("after_sequence"), 0)),
		Limit:         parseInt(values.Get("limit"), obspkg.DefaultEventQueryLimit),
	}
}

func parseInt(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
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

func writeError(w nethttp.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeSSE(w nethttp.ResponseWriter, flusher nethttp.Flusher, record obspkg.EventRecord) bool {
	data, err := json.Marshal(record)
	if err != nil {
		writeSSEError(w, flusher, err)
		return false
	}
	if _, err := fmt.Fprintf(w, "event: runtime_event\ndata: %s\n\n", data); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func writeSSEError(w nethttp.ResponseWriter, flusher nethttp.Flusher, err error) {
	data, _ := json.Marshal(map[string]string{"error": err.Error()})
	_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", data)
	flusher.Flush()
}
