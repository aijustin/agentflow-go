package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	agentflow "github.com/aijustin/agentflow-go"
	observabilityhttp "github.com/aijustin/agentflow-go/internal/adapter/observability/http"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/graph"
	"github.com/aijustin/agentflow-go/pkg/log"
	"github.com/aijustin/agentflow-go/pkg/observability"
)

type ObservabilityHTTPHandlerConfig struct {
	Store          observability.EventStore
	Hub            *observability.EventHub
	AuthMiddleware func(http.Handler) http.Handler
	// Framework enables Studio graph export, step listing, and resume-from-step.
	Framework *agentflow.Framework
	// StudioSavePath enables POST /observability/api/studio/save for the configured scenario file.
	StudioSavePath string
	// TraceExploreURL is an optional trace UI link template, e.g. https://jaeger.example.com/trace/{trace_id}.
	TraceExploreURL string
	// InsecureAllowNoAuth disables the default-deny guard on mutating
	// endpoints (HITL resume, resume-from-step/checkpoint, fork, studio
	// run/save) when AuthMiddleware is nil. Only set it behind an
	// authenticating reverse proxy or in tests.
	InsecureAllowNoAuth bool
	// Logger receives the one-time construction warning emitted when
	// AuthMiddleware is nil; nil discards it.
	Logger log.Logger
}

func NewObservabilityHTTPHandler(config ObservabilityHTTPHandlerConfig) (http.Handler, error) {
	httpConfig := observabilityhttp.Config{
		Store:               config.Store,
		Hub:                 config.Hub,
		AuthMiddleware:      config.AuthMiddleware,
		TraceExploreURL:     config.TraceExploreURL,
		InsecureAllowNoAuth: config.InsecureAllowNoAuth,
		Logger:              config.Logger,
	}
	if config.Framework != nil {
		adapter := &studioFramework{framework: config.Framework, savePath: config.StudioSavePath}
		httpConfig.Steps = adapter
		httpConfig.HITLResume = adapter
		httpConfig.Graph = adapter
		httpConfig.Resume = adapter
		httpConfig.History = adapter
		httpConfig.Checkpoints = adapter
		httpConfig.Restore = adapter
		httpConfig.Studio = adapter
		httpConfig.Codegen = adapter
		httpConfig.YAML = adapter
		httpConfig.ImportYAML = adapter
		httpConfig.RunStudio = adapter
		httpConfig.Compose = adapter
		httpConfig.Parts = adapter
		if config.StudioSavePath != "" {
			httpConfig.StudioSave = adapter
		}
		httpConfig.Compare = adapter
		httpConfig.Thread = adapter
		httpConfig.Fork = adapter
	}
	return observabilityhttp.NewHandler(httpConfig)
}

// --- Studio Framework Adapter ---

// --- Studio Framework Adapter ---

type studioFramework struct {
	framework *agentflow.Framework
	savePath  string
}

func (adapter *studioFramework) ListRunSteps(ctx context.Context, runID string) (any, error) {
	return adapter.framework.ListRunSteps(ctx, runID)
}

func (adapter *studioFramework) ResumeRunHITL(ctx context.Context, runID string, decision core.Decision, amendment json.RawMessage, continueExecution bool) (any, error) {
	return adapter.framework.ResumeRunByID(ctx, runID, decision, amendment, continueExecution)
}

func (adapter *studioFramework) ResumeFromStep(ctx context.Context, runID, nodeID string) (any, error) {
	return adapter.framework.ResumeFromStep(ctx, runID, nodeID)
}

func (adapter *studioFramework) ListRunCheckpoints(ctx context.Context, runID string, limit int) (any, error) {
	return adapter.framework.ListRunCheckpoints(ctx, runID, limit)
}

func (adapter *studioFramework) GetRunCheckpoint(ctx context.Context, runID string, version int64) (any, error) {
	return adapter.framework.GetRunCheckpoint(ctx, runID, version)
}

func (adapter *studioFramework) ResumeFromCheckpoint(ctx context.Context, runID string, version int64) (any, error) {
	return adapter.framework.ResumeFromCheckpoint(ctx, runID, version)
}

func (adapter *studioFramework) ExportScenarioGraph() any {
	return adapter.framework.ExportScenarioGraph()
}

func (adapter *studioFramework) ValidateStudioGraph(ctx context.Context, edited any) (any, error) {
	graph, err := decodeStudioGraph(edited)
	if err != nil {
		return nil, err
	}
	return adapter.framework.ValidateStudioGraph(ctx, graph)
}

func (adapter *studioFramework) GenerateStudioBuilderCode(ctx context.Context, edited any) (any, error) {
	graph, err := decodeStudioGraph(edited)
	if err != nil {
		return nil, err
	}
	return adapter.framework.GenerateStudioBuilderCode(ctx, graph)
}

func (adapter *studioFramework) GenerateStudioScenarioYAML(ctx context.Context, edited any) (any, error) {
	graph, err := decodeStudioGraph(edited)
	if err != nil {
		return nil, err
	}
	return adapter.framework.GenerateStudioScenarioYAML(ctx, graph)
}

func (adapter *studioFramework) ImportStudioScenarioYAML(ctx context.Context, yamlData []byte, layout any) (any, error) {
	var layoutGraph graph.ScenarioGraph
	if layout != nil {
		var err error
		layoutGraph, err = decodeStudioGraph(layout)
		if err != nil {
			return nil, err
		}
	}
	return adapter.framework.ImportStudioScenarioYAML(ctx, yamlData, layoutGraph)
}

func (adapter *studioFramework) RunStudioGraph(ctx context.Context, edited any, req any) (any, error) {
	graph, err := decodeStudioGraph(edited)
	if err != nil {
		return nil, err
	}
	runReq, err := decodeStudioRunRequest(req)
	if err != nil {
		return nil, err
	}
	draft, err := decodeStudioScenario(req)
	if err != nil {
		return nil, err
	}
	return adapter.framework.RunStudioGraphWithScenario(ctx, graph, draft, runReq)
}

func decodeStudioRunRequest(value any) (agentflow.RunRequest, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return agentflow.RunRequest{}, fmt.Errorf("studio run request: encode: %w", err)
	}
	var req agentflow.RunRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return agentflow.RunRequest{}, fmt.Errorf("studio run request: decode: %w", err)
	}
	return req, nil
}

func (adapter *studioFramework) ComposeStudioGraph(ctx context.Context, value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("studio compose request: encode: %w", err)
	}
	var req agentflow.ComposeGraphRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("studio compose request: decode: %w", err)
	}
	return adapter.framework.ComposeGraph(ctx, req)
}

func (adapter *studioFramework) ListStudioParts() any {
	return adapter.framework.StudioParts()
}

func (adapter *studioFramework) CompareRuns(ctx context.Context, runA, runB string) (any, error) {
	return adapter.framework.CompareRuns(ctx, runA, runB)
}

func (adapter *studioFramework) ListRunThread(ctx context.Context, runID string) (any, error) {
	return adapter.framework.ListRunThread(ctx, runID)
}

func (adapter *studioFramework) ForkRun(ctx context.Context, runID string, version int64) (any, error) {
	return adapter.framework.ForkRun(ctx, runID, version)
}

func (adapter *studioFramework) SaveStudioGraph(ctx context.Context, edited any) (any, error) {
	if adapter.savePath == "" {
		return nil, fmt.Errorf("studio save path is not configured")
	}
	graph, draft, err := decodeStudioSaveRequest(edited)
	if err != nil {
		return nil, err
	}
	return adapter.framework.SaveStudioGraphWithScenario(ctx, graph, draft, adapter.savePath)
}

func decodeStudioSaveRequest(value any) (graph.ScenarioGraph, *core.Scenario, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return graph.ScenarioGraph{}, nil, fmt.Errorf("studio save request: encode: %w", err)
	}
	var envelope struct {
		Graph    json.RawMessage `json:"graph"`
		Scenario json.RawMessage `json:"scenario"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return graph.ScenarioGraph{}, nil, fmt.Errorf("studio save request: decode: %w", err)
	}
	graphRaw := raw
	if len(envelope.Graph) > 0 && string(envelope.Graph) != "null" {
		graphRaw = envelope.Graph
	}
	var edited graph.ScenarioGraph
	if err := json.Unmarshal(graphRaw, &edited); err != nil {
		return graph.ScenarioGraph{}, nil, fmt.Errorf("studio graph: decode: %w", err)
	}
	draft, err := decodeStudioScenarioRaw(envelope.Scenario)
	if err != nil {
		return graph.ScenarioGraph{}, nil, err
	}
	return edited, draft, nil
}

func decodeStudioScenario(value any) (*core.Scenario, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("studio scenario: encode: %w", err)
	}
	var envelope struct {
		Scenario json.RawMessage `json:"scenario"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("studio scenario: decode envelope: %w", err)
	}
	return decodeStudioScenarioRaw(envelope.Scenario)
}

func decodeStudioScenarioRaw(raw json.RawMessage) (*core.Scenario, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var draft core.Scenario
	if err := json.Unmarshal(raw, &draft); err != nil {
		return nil, fmt.Errorf("studio scenario: decode: %w", err)
	}
	return &draft, nil
}

func decodeStudioGraph(value any) (graph.ScenarioGraph, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return graph.ScenarioGraph{}, fmt.Errorf("studio graph: encode: %w", err)
	}
	var out graph.ScenarioGraph
	if err := json.Unmarshal(raw, &out); err != nil {
		return graph.ScenarioGraph{}, fmt.Errorf("studio graph: decode: %w", err)
	}
	return out, nil
}
