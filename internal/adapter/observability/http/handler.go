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
	History        CheckpointLister
	Checkpoints    CheckpointLoader
	Restore        CheckpointResumer
	Studio         StudioValidator
	Codegen        StudioCodeGenerator
	Compare        RunComparer
	Thread         ThreadLister
	Fork           RunForker
}

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

type GraphExporter interface {
	ExportScenarioGraph() any
}

type StudioValidator interface {
	ValidateStudioGraph(ctx context.Context, graph any) (any, error)
}

type StudioCodeGenerator interface {
	GenerateStudioBuilderCode(ctx context.Context, graph any) (any, error)
}

type RunComparer interface {
	CompareRuns(ctx context.Context, runA, runB string) (any, error)
}

type ThreadLister interface {
	ListRunThread(ctx context.Context, runID string) (any, error)
}

type RunForker interface {
	ForkRun(ctx context.Context, runID string, version int64) (any, error)
}

type Handler struct {
	store      obspkg.EventStore
	hub        *obspkg.EventHub
	steps      StepsLister
	graph      GraphExporter
	resume     StepResumer
	history    CheckpointLister
	checkpoint CheckpointLoader
	restore    CheckpointResumer
	studio     StudioValidator
	codegen    StudioCodeGenerator
	compare    RunComparer
	thread     ThreadLister
	fork       RunForker
	mux        *nethttp.ServeMux
	handler    nethttp.Handler
}

func NewHandler(config Config) (*Handler, error) {
	if config.Store == nil {
		return nil, fmt.Errorf("observability http: event store is nil")
	}
	handler := &Handler{
		store:      config.Store,
		hub:        config.Hub,
		steps:      config.Steps,
		graph:      config.Graph,
		resume:     config.Resume,
		history:    config.History,
		checkpoint: config.Checkpoints,
		restore:    config.Restore,
		studio:     config.Studio,
		codegen:    config.Codegen,
		compare:    config.Compare,
		thread:     config.Thread,
		fork:       config.Fork,
		mux:        nethttp.NewServeMux(),
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
	handler.mux.HandleFunc("/api/compare", handler.handleCompare)
	handler.mux.HandleFunc("/api/studio/validate", handler.handleStudioValidate)
	handler.mux.HandleFunc("/api/studio/codegen", handler.handleStudioCodegen)
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
	runID, segments, ok := parseRunResource(r.URL.Path)
	if !ok {
		nethttp.NotFound(w, r)
		return
	}
	switch {
	case len(segments) == 1 && segments[0] == "events":
		handler.handleEvents(w, r, runID)
	case len(segments) == 1 && segments[0] == "stream":
		handler.handleStream(w, r, runID)
	case len(segments) == 1 && segments[0] == "steps":
		handler.handleSteps(w, r, runID)
	case len(segments) == 1 && segments[0] == "resume-from-step":
		handler.handleResumeFromStep(w, r, runID)
	case len(segments) == 1 && segments[0] == "checkpoints":
		handler.handleCheckpoints(w, r, runID)
	case len(segments) == 2 && segments[0] == "checkpoints":
		handler.handleCheckpointVersion(w, r, runID, segments[1])
	case len(segments) == 1 && segments[0] == "resume-from-checkpoint":
		handler.handleResumeFromCheckpoint(w, r, runID)
	case len(segments) == 1 && segments[0] == "thread":
		handler.handleRunThread(w, r, runID)
	case len(segments) == 1 && segments[0] == "fork":
		handler.handleRunFork(w, r, runID)
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

func (handler *Handler) handleCheckpoints(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	if r.Method != nethttp.MethodGet {
		methodNotAllowed(w, nethttp.MethodGet)
		return
	}
	if handler.history == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("checkpoint history is not configured"))
		return
	}
	limit := parseInt(r.URL.Query().Get("limit"), 0)
	result, err := handler.history.ListRunCheckpoints(r.Context(), runID, limit)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (handler *Handler) handleCheckpointVersion(w nethttp.ResponseWriter, r *nethttp.Request, runID, versionRaw string) {
	if r.Method != nethttp.MethodGet {
		methodNotAllowed(w, nethttp.MethodGet)
		return
	}
	if handler.checkpoint == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("checkpoint loading is not configured"))
		return
	}
	version, err := strconv.ParseInt(strings.TrimSpace(versionRaw), 10, 64)
	if err != nil || version <= 0 {
		writeError(w, nethttp.StatusBadRequest, fmt.Errorf("version must be a positive integer"))
		return
	}
	result, err := handler.checkpoint.GetRunCheckpoint(r.Context(), runID, version)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (handler *Handler) handleResumeFromCheckpoint(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if handler.restore == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("resume-from-checkpoint is not configured"))
		return
	}
	var body struct {
		Version int64 `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, nethttp.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	if body.Version <= 0 {
		writeError(w, nethttp.StatusBadRequest, fmt.Errorf("version must be a positive integer"))
		return
	}
	result, err := handler.restore.ResumeFromCheckpoint(r.Context(), runID, body.Version)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (handler *Handler) handleCompare(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		methodNotAllowed(w, nethttp.MethodGet)
		return
	}
	if handler.compare == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("run compare is not configured"))
		return
	}
	runA := strings.TrimSpace(r.URL.Query().Get("run_a"))
	runB := strings.TrimSpace(r.URL.Query().Get("run_b"))
	if runA == "" || runB == "" {
		writeError(w, nethttp.StatusBadRequest, fmt.Errorf("run_a and run_b are required"))
		return
	}
	result, err := handler.compare.CompareRuns(r.Context(), runA, runB)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (handler *Handler) handleStudioValidate(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if handler.studio == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("studio validate is not configured"))
		return
	}
	graph, err := decodeScenarioGraph(r.Body)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err)
		return
	}
	result, err := handler.studio.ValidateStudioGraph(r.Context(), graph)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (handler *Handler) handleStudioCodegen(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if handler.codegen == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("studio codegen is not configured"))
		return
	}
	graph, err := decodeScenarioGraph(r.Body)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err)
		return
	}
	result, err := handler.codegen.GenerateStudioBuilderCode(r.Context(), graph)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (handler *Handler) handleRunThread(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	if r.Method != nethttp.MethodGet {
		methodNotAllowed(w, nethttp.MethodGet)
		return
	}
	if handler.thread == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("run thread listing is not configured"))
		return
	}
	result, err := handler.thread.ListRunThread(r.Context(), runID)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"thread_id": runID, "runs": result})
}

func (handler *Handler) handleRunFork(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if handler.fork == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("run fork is not configured"))
		return
	}
	var body struct {
		Version int64 `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil && err != io.EOF {
		writeError(w, nethttp.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	result, err := handler.fork.ForkRun(r.Context(), runID, body.Version)
	if err != nil {
		writeError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func decodeScenarioGraph(body io.Reader) (any, error) {
	var graph any
	if err := json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&graph); err != nil {
		return nil, fmt.Errorf("decode graph: %w", err)
	}
	return graph, nil
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

func parseRunResource(path string) (string, []string, bool) {
	suffix := strings.TrimPrefix(path, "/api/runs/")
	parts := strings.Split(strings.Trim(suffix, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", nil, false
	}
	runID, err := url.PathUnescape(parts[0])
	if err != nil || runID == "" {
		return "", nil, false
	}
	return runID, parts[1:], true
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
