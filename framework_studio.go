package agentflow

import (
	"context"
	"fmt"
	"sort"

	configyaml "github.com/aijustin/agentflow-go/internal/adapter/config/yaml"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/graph"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/studio"
)

// ValidateStudioResult reports graph/scenario validation output for Studio.
type ValidateStudioResult struct {
	Valid        bool   `json:"valid"`
	Error        string `json:"error,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	Scenario     string `json:"scenario_name"`
}

// SaveStudioResult describes a persisted Studio graph write.
type SaveStudioResult struct {
	Path         string              `json:"path"`
	ScenarioName string              `json:"scenario_name"`
	Graph        graph.ScenarioGraph `json:"graph,omitempty"`
}

// CodegenResult contains generated builder code for a Studio graph.
type CodegenResult struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

// ThreadRunSummary describes one run in a fork/thread group.
type ThreadRunSummary struct {
	RunID           string             `json:"run_id"`
	ParentRunID     string             `json:"parent_run_id,omitempty"`
	ForkFromVersion int64              `json:"fork_from_version,omitempty"`
	ThreadID        string             `json:"thread_id"`
	Status          runstate.RunStatus `json:"status"`
	ScenarioName    string             `json:"scenario_name,omitempty"`
}

type ForkRunResult struct {
	RunID           string `json:"run_id"`
	ParentRunID     string `json:"parent_run_id"`
	ThreadID        string `json:"thread_id"`
	ForkFromVersion int64  `json:"fork_from_version,omitempty"`
}

// ValidateStudioGraph validates an edited Studio graph against the framework scenario.
func (f *Framework) ValidateStudioGraph(_ context.Context, edited graph.ScenarioGraph) (ValidateStudioResult, error) {
	scenario, err := graph.ApplyGraph(f.scenario, edited)
	if err != nil {
		payload := studio.ErrorPayloadFrom(studio.WrapGraphError(err))
		return ValidateStudioResult{Valid: false, Error: payload.Message, ErrorCode: payload.Code, Scenario: f.scenario.Name}, nil
	}
	if err := ValidateScenario(scenario); err != nil {
		payload := studio.ErrorPayloadFrom(err)
		return ValidateStudioResult{Valid: false, Error: payload.Message, ErrorCode: payload.Code, Scenario: scenario.Name}, nil
	}
	return ValidateStudioResult{Valid: true, Scenario: scenario.Name}, nil
}

// GenerateStudioBuilderCode renders builder Go code for an edited Studio graph.
func (f *Framework) GenerateStudioBuilderCode(_ context.Context, edited graph.ScenarioGraph) (CodegenResult, error) {
	scenario, err := graph.ApplyGraph(f.scenario, edited)
	if err != nil {
		return CodegenResult{}, err
	}
	code, err := graph.GenerateBuilderCode(scenario)
	if err != nil {
		return CodegenResult{}, err
	}
	return CodegenResult{Language: "go", Code: code}, nil
}

// GenerateStudioScenarioYAML renders legacy scenario YAML for an edited Studio graph.
func (f *Framework) GenerateStudioScenarioYAML(_ context.Context, edited graph.ScenarioGraph) (CodegenResult, error) {
	scenario, err := graph.ApplyGraph(f.scenario, edited)
	if err != nil {
		return CodegenResult{}, err
	}
	yamlDoc, err := configyaml.Marshal(scenario)
	if err != nil {
		return CodegenResult{}, err
	}
	return CodegenResult{Language: "yaml", Code: string(yamlDoc)}, nil
}

// SaveStudioGraph validates an edited graph, writes legacy YAML to path, and updates the framework scenario.
func (f *Framework) SaveStudioGraph(ctx context.Context, edited graph.ScenarioGraph, path string) (SaveStudioResult, error) {
	if path == "" {
		return SaveStudioResult{}, &studio.CodedError{Code: "studio.save_path_missing", Message: "agentflow: studio save path is required"}
	}
	scenario, err := graph.ApplyGraph(f.scenario, edited)
	if err != nil {
		return SaveStudioResult{}, studio.WrapGraphError(err)
	}
	if err := ValidateScenario(scenario); err != nil {
		return SaveStudioResult{}, err
	}
	if err := configyaml.SaveFile(path, scenario); err != nil {
		return SaveStudioResult{}, err
	}
	f.scenario = scenario
	return SaveStudioResult{
		Path:         path,
		ScenarioName: scenario.Name,
		Graph:        graph.MergeLayout(edited, graph.ExportScenario(scenario)),
	}, nil
}

// RunStudioGraph validates an edited graph and executes it as a new run.
func (f *Framework) RunStudioGraph(ctx context.Context, edited graph.ScenarioGraph, req RunRequest) (RunResult, error) {
	scenario, err := graph.ApplyGraph(f.scenario, edited)
	if err != nil {
		return RunResult{}, err
	}
	if err := ValidateScenario(scenario); err != nil {
		return RunResult{}, err
	}
	switch scenario.Orchestration.Mode {
	case core.OrchestrationFixedWorkflow:
		return f.runWorkflowScenario(ctx, scenario, req)
	case core.OrchestrationHybrid:
		if scenario.Orchestration.Workflow == nil {
			return f.engine.Run(ctx, req)
		}
		req, paused, err := f.prepareHybridAutonomousRunScenario(ctx, scenario, req)
		if err != nil || paused.Status != "" {
			return paused, err
		}
		return f.engine.RunHybrid(ctx, req)
	default:
		return RunResult{}, fmt.Errorf("agentflow: studio run supports fixed_workflow and hybrid scenarios")
	}
}

// CompareRuns diffs step outputs between two persisted runs.
func (f *Framework) CompareRuns(ctx context.Context, runA, runB string) (studio.RunCompareResult, error) {
	if f.runs == nil {
		return studio.RunCompareResult{}, fmt.Errorf("agentflow: run-state repository is not configured")
	}
	if runA == "" || runB == "" {
		return studio.RunCompareResult{}, fmt.Errorf("agentflow: compare requires run_a and run_b")
	}
	snapA, err := runstate.LoadAuthorized(ctx, f.runs, runA)
	if err != nil {
		return studio.RunCompareResult{}, err
	}
	snapB, err := runstate.LoadAuthorized(ctx, f.runs, runB)
	if err != nil {
		return studio.RunCompareResult{}, err
	}
	return studio.CompareSnapshots(runA, runB, snapA, snapB), nil
}

// ListRunThread returns runs in the same fork/thread group as the given run.
func (f *Framework) ListRunThread(ctx context.Context, runID string) ([]ThreadRunSummary, error) {
	if f.runs == nil {
		return nil, fmt.Errorf("agentflow: run-state repository is not configured")
	}
	root, err := runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return nil, err
	}
	threadID := resolveThreadID(root)
	list, err := f.runs.List(ctx, runstate.ListFilter{ThreadID: threadID})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		list = []runstate.RunSnapshot{root}
	}
	out := make([]ThreadRunSummary, 0, len(list))
	for _, snap := range list {
		out = append(out, ThreadRunSummary{
			RunID:           snap.RunID,
			ParentRunID:     snap.ParentRunID,
			ForkFromVersion: snap.ForkFromVersion,
			ThreadID:        resolveThreadID(snap),
			Status:          snap.Status,
			ScenarioName:    snap.ScenarioName,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RunID < out[j].RunID })
	return out, nil
}

// ForkRun copies a run snapshot into a new run ID without modifying the parent run.
func (f *Framework) ForkRun(ctx context.Context, parentRunID string, version int64) (ForkRunResult, error) {
	if f.runs == nil {
		return ForkRunResult{}, fmt.Errorf("agentflow: run-state repository is not configured")
	}
	parent, err := runstate.LoadAuthorized(ctx, f.runs, parentRunID)
	if err != nil {
		return ForkRunResult{}, err
	}
	source := parent
	if version > 0 {
		if f.checkpointHistory == nil {
			return ForkRunResult{}, fmt.Errorf("agentflow: checkpoint history is not configured")
		}
		source, err = f.checkpointHistory.Load(ctx, parentRunID, version)
		if err != nil {
			return ForkRunResult{}, err
		}
	}
	newRunID := generateRunID()
	threadID := resolveThreadID(parent)
	child := source
	child.RunID = newRunID
	child.Version = 0
	child.ParentRunID = parentRunID
	child.ForkFromVersion = version
	child.ThreadID = threadID
	child.PendingGate = nil
	if err := f.runs.Save(ctx, &child, 0); err != nil {
		return ForkRunResult{}, err
	}
	return ForkRunResult{
		RunID:           newRunID,
		ParentRunID:     parentRunID,
		ThreadID:        threadID,
		ForkFromVersion: version,
	}, nil
}

func resolveThreadID(snapshot runstate.RunSnapshot) string {
	return runstate.ResolveThreadID(snapshot)
}
