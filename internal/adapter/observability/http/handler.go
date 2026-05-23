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
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/studio"
)

type Config struct {
	Store          obspkg.EventStore
	Hub            *obspkg.EventHub
	AuthMiddleware func(nethttp.Handler) nethttp.Handler
	TraceExploreURL string
	Steps          StepsLister
	HITLResume     RunHITLResumer
	Graph          GraphExporter
	Resume         StepResumer
	History        CheckpointLister
	Checkpoints    CheckpointLoader
	Restore        CheckpointResumer
	Studio         StudioValidator
	Codegen        StudioCodeGenerator
	YAML           StudioYAMLExporter
	ImportYAML     StudioYAMLImporter
	RunStudio      StudioRunner
	StudioSave     StudioSaver
	Compare        RunComparer
	Thread         ThreadLister
	Fork           RunForker
}

type StepsLister interface {
	ListRunSteps(ctx context.Context, runID string) (any, error)
}

type RunHITLResumer interface {
	ResumeRunHITL(ctx context.Context, runID string, decision core.Decision, amendment json.RawMessage, continueExecution bool) (any, error)
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

type StudioYAMLExporter interface {
	GenerateStudioScenarioYAML(ctx context.Context, graph any) (any, error)
}

type StudioYAMLImporter interface {
	ImportStudioScenarioYAML(ctx context.Context, yaml []byte, layout any) (any, error)
}

type StudioRunner interface {
	RunStudioGraph(ctx context.Context, graph any, req any) (any, error)
}

type StudioSaver interface {
	SaveStudioGraph(ctx context.Context, graph any) (any, error)
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
	hitlResume RunHITLResumer
	graph      GraphExporter
	resume     StepResumer
	history    CheckpointLister
	checkpoint CheckpointLoader
	restore    CheckpointResumer
	studio     StudioValidator
	codegen    StudioCodeGenerator
	yaml       StudioYAMLExporter
	importYAML StudioYAMLImporter
	runStudio  StudioRunner
	studioSave StudioSaver
	compare    RunComparer
	thread     ThreadLister
	fork       RunForker
	traceExploreURL string
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
		hitlResume: config.HITLResume,
		graph:      config.Graph,
		resume:     config.Resume,
		history:    config.History,
		checkpoint: config.Checkpoints,
		restore:    config.Restore,
		studio:     config.Studio,
		codegen:    config.Codegen,
		yaml:       config.YAML,
		importYAML: config.ImportYAML,
		runStudio:  config.RunStudio,
		studioSave: config.StudioSave,
		compare:    config.Compare,
		thread:     config.Thread,
		fork:       config.Fork,
		traceExploreURL: config.TraceExploreURL,
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
	handler.mux.HandleFunc("/api/ui-config", handler.handleUIConfig)
	handler.mux.HandleFunc("/api/graph", handler.handleGraph)
	handler.mux.HandleFunc("/api/compare", handler.handleCompare)
	handler.mux.HandleFunc("/api/studio/validate", handler.handleStudioValidate)
	handler.mux.HandleFunc("/api/studio/codegen", handler.handleStudioCodegen)
	handler.mux.HandleFunc("/api/studio/yaml", handler.handleStudioYAML)
	handler.mux.HandleFunc("/api/studio/import-yaml", handler.handleStudioImportYAML)
	handler.mux.HandleFunc("/api/studio/run", handler.handleStudioRun)
	handler.mux.HandleFunc("/api/studio/save", handler.handleStudioSave)
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

func (handler *Handler) handleUIConfig(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		methodNotAllowed(w, nethttp.MethodGet)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]string{
		"trace_explore_url": handler.traceExploreURL,
	})
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
	case len(segments) == 1 && segments[0] == "hitl/resume":
		handler.handleRunHITLResume(w, r, runID)
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

func (handler *Handler) handleRunHITLResume(w nethttp.ResponseWriter, r *nethttp.Request, runID string) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if handler.hitlResume == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("run HITL resume is not configured"))
		return
	}
	var body struct {
		Decision  core.Decision   `json:"decision"`
		Amendment json.RawMessage `json:"amendment,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, nethttp.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	if !body.Decision.Valid() {
		writeError(w, nethttp.StatusBadRequest, fmt.Errorf("valid decision is required"))
		return
	}
	result, err := handler.hitlResume.ResumeRunHITL(r.Context(), runID, body.Decision, body.Amendment, true)
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
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	result, err := handler.studio.ValidateStudioGraph(r.Context(), graph)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
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
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	result, err := handler.codegen.GenerateStudioBuilderCode(r.Context(), graph)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (handler *Handler) handleStudioYAML(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if handler.yaml == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("studio yaml export is not configured"))
		return
	}
	graph, err := decodeScenarioGraph(r.Body)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	result, err := handler.yaml.GenerateStudioScenarioYAML(r.Context(), graph)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (handler *Handler) handleStudioImportYAML(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if handler.importYAML == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("studio yaml import is not configured"))
		return
	}
	var body struct {
		YAML        string `json:"yaml"`
		LayoutGraph any    `json:"layout_graph"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, nethttp.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	if strings.TrimSpace(body.YAML) == "" {
		writeError(w, nethttp.StatusBadRequest, fmt.Errorf("yaml is required"))
		return
	}
	result, err := handler.importYAML.ImportStudioScenarioYAML(r.Context(), []byte(body.YAML), body.LayoutGraph)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (handler *Handler) handleStudioRun(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if handler.runStudio == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("studio run is not configured"))
		return
	}
	var body struct {
		Graph  any    `json:"graph"`
		Prompt string `json:"prompt"`
		Agent  string `json:"agent"`
		RunID  string `json:"run_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, nethttp.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	if body.Graph == nil {
		writeError(w, nethttp.StatusBadRequest, fmt.Errorf("graph is required"))
		return
	}
	req := map[string]any{
		"prompt": strings.TrimSpace(body.Prompt),
		"agent":  strings.TrimSpace(body.Agent),
		"run_id": strings.TrimSpace(body.RunID),
	}
	result, err := handler.runStudio.RunStudioGraph(r.Context(), body.Graph, req)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (handler *Handler) handleStudioSave(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if handler.studioSave == nil {
		writeError(w, nethttp.StatusNotImplemented, fmt.Errorf("studio save is not configured"))
		return
	}
	graph, err := decodeScenarioGraph(r.Body)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	result, err := handler.studioSave.SaveStudioGraph(r.Context(), graph)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
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
	writeStudioError(w, status, err)
}

func writeStudioError(w nethttp.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": studio.ErrorPayloadFrom(err)})
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
	data, _ := json.Marshal(map[string]any{"error": studio.ErrorPayloadFrom(err)})
	_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", data)
	flusher.Flush()
}
