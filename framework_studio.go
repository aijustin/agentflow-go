package agentflow

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	configyaml "github.com/aijustin/agentflow-go/internal/adapter/config/yaml"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/graph"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/studio"
)

// ValidateStudioResult reports graph/scenario validation output for Studio.
type ValidateStudioResult struct {
	Valid     bool   `json:"valid"`
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
	Scenario  string `json:"scenario_name"`
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

// Studio groups development-time graph editing and run inspection APIs.
// Prefer Framework.Studio() for new call sites; existing Framework methods remain
// as thin delegates for compatibility.
type Studio struct {
	f *Framework
}

// Studio returns the development-time Studio API bound to this framework.
func (f *Framework) Studio() Studio {
	return Studio{f: f}
}

// StudioPart describes one composable part of the live scenario.
type StudioPart struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// StudioParts groups the live scenario's composable parts by kind, sorted by
// name. It backs the Studio parts palette and AI composition catalog.
type StudioParts struct {
	Agents    []StudioPart `json:"agents"`
	Tools     []StudioPart `json:"tools"`
	Skills    []StudioPart `json:"skills"`
	Subgraphs []StudioPart `json:"subgraphs"`
}

// Parts lists the composable parts of the live scenario.
func (s Studio) Parts() StudioParts {
	scenario := s.f.currentScenario()
	parts := StudioParts{
		Agents:    make([]StudioPart, 0, len(scenario.Agents)),
		Tools:     make([]StudioPart, 0, len(scenario.Tools)),
		Skills:    make([]StudioPart, 0, len(scenario.Skills)),
		Subgraphs: make([]StudioPart, 0, len(scenario.Orchestration.Workflows)),
	}
	for name, agent := range scenario.Agents {
		parts.Agents = append(parts.Agents, StudioPart{Name: name, Description: agent.Description})
	}
	for name, tool := range scenario.Tools {
		parts.Tools = append(parts.Tools, StudioPart{Name: name, Description: tool.Description})
	}
	for name, skill := range scenario.Skills {
		parts.Skills = append(parts.Skills, StudioPart{Name: name, Description: skill.Description})
	}
	for name := range scenario.Orchestration.Workflows {
		parts.Subgraphs = append(parts.Subgraphs, StudioPart{Name: name})
	}
	sortParts := func(list []StudioPart) {
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	}
	sortParts(parts.Agents)
	sortParts(parts.Tools)
	sortParts(parts.Skills)
	sortParts(parts.Subgraphs)
	return parts
}

// ValidateStudioGraph validates an edited Studio graph against the framework scenario.
func (s Studio) ValidateStudioGraph(ctx context.Context, edited graph.ScenarioGraph) (ValidateStudioResult, error) {
	return s.ValidateStudioGraphWithScenario(ctx, edited, nil)
}

// ValidateStudioGraphWithScenario validates a graph together with the
// additive agents/skills returned by scenario-mode composition.
func (s Studio) ValidateStudioGraphWithScenario(_ context.Context, edited graph.ScenarioGraph, draft *core.Scenario) (ValidateStudioResult, error) {
	scenario, err := resolveStudioScenario(s.f.currentScenario(), edited, draft)
	if err != nil {
		payload := studio.ErrorPayloadFrom(studio.WrapGraphError(err))
		return ValidateStudioResult{Valid: false, Error: payload.Message, ErrorCode: payload.Code, Scenario: s.f.currentScenario().Name}, nil
	}
	if err := ValidateScenario(scenario); err != nil {
		payload := studio.ErrorPayloadFrom(err)
		return ValidateStudioResult{Valid: false, Error: payload.Message, ErrorCode: payload.Code, Scenario: scenario.Name}, nil
	}
	return ValidateStudioResult{Valid: true, Scenario: scenario.Name}, nil
}

// GenerateStudioBuilderCode renders builder Go code for an edited Studio graph.
func (s Studio) GenerateStudioBuilderCode(ctx context.Context, edited graph.ScenarioGraph) (CodegenResult, error) {
	return s.GenerateStudioBuilderCodeWithScenario(ctx, edited, nil)
}

// GenerateStudioBuilderCodeWithScenario renders builder code including an
// optional additive scenario-mode composition draft.
func (s Studio) GenerateStudioBuilderCodeWithScenario(_ context.Context, edited graph.ScenarioGraph, draft *core.Scenario) (CodegenResult, error) {
	scenario, err := resolveStudioScenario(s.f.currentScenario(), edited, draft)
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
func (s Studio) GenerateStudioScenarioYAML(ctx context.Context, edited graph.ScenarioGraph) (CodegenResult, error) {
	return s.GenerateStudioScenarioYAMLWithScenario(ctx, edited, nil)
}

// GenerateStudioScenarioYAMLWithScenario renders YAML including an optional
// additive scenario-mode composition draft.
func (s Studio) GenerateStudioScenarioYAMLWithScenario(_ context.Context, edited graph.ScenarioGraph, draft *core.Scenario) (CodegenResult, error) {
	scenario, err := resolveStudioScenario(s.f.currentScenario(), edited, draft)
	if err != nil {
		return CodegenResult{}, err
	}
	yamlDoc, err := configyaml.Marshal(scenario)
	if err != nil {
		return CodegenResult{}, err
	}
	return CodegenResult{Language: "yaml", Code: string(yamlDoc)}, nil
}

// ImportStudioResult describes a YAML import into an editable Studio graph.
type ImportStudioResult struct {
	ScenarioName string              `json:"scenario_name"`
	Graph        graph.ScenarioGraph `json:"graph"`
}

// ImportStudioScenarioYAML parses legacy scenario YAML and returns an editable graph.
// When layout is non-empty, node positions from layout are merged onto the imported graph.
func (s Studio) ImportStudioScenarioYAML(_ context.Context, yamlData []byte, layout graph.ScenarioGraph) (ImportStudioResult, error) {
	scenario, err := configyaml.Load(yamlData)
	if err != nil {
		return ImportStudioResult{}, err
	}
	if err := ValidateScenario(scenario); err != nil {
		return ImportStudioResult{}, err
	}
	exported := graph.ExportScenario(scenario)
	if (layout.Workflow != nil && len(layout.Workflow.Nodes) > 0) || len(layout.Workflows) > 0 {
		exported = graph.MergeLayout(layout, exported)
	}
	return ImportStudioResult{ScenarioName: scenario.Name, Graph: exported}, nil
}

// SaveStudioGraph validates an edited graph, writes legacy YAML to path, and
// atomically replaces the live scenario and engine so subsequent runs use the
// saved definition (not a half-updated Framework / stale Engine pair).
func (s Studio) SaveStudioGraph(ctx context.Context, edited graph.ScenarioGraph, path string) (SaveStudioResult, error) {
	return s.SaveStudioGraphWithScenario(ctx, edited, nil, path)
}

// SaveStudioGraphWithScenario persists a graph together with the additive
// agents/skills returned by scenario-mode ComposeGraph. Existing live parts
// are immutable; a stale or tampered draft that changes them is rejected.
func (s Studio) SaveStudioGraphWithScenario(ctx context.Context, edited graph.ScenarioGraph, draft *core.Scenario, path string) (SaveStudioResult, error) {
	if path == "" {
		return SaveStudioResult{}, &studio.CodedError{Code: "studio.save_path_missing", Message: "agentflow: studio save path is required"}
	}
	base := s.f.currentScenario()
	scenario, err := resolveStudioScenario(base, edited, draft)
	if err != nil {
		return SaveStudioResult{}, studio.WrapGraphError(err)
	}
	if err := ValidateScenario(scenario); err != nil {
		return SaveStudioResult{}, err
	}
	if err := configyaml.SaveFile(path, scenario); err != nil {
		return SaveStudioResult{}, err
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	engine, err := s.f.rebuildLiveEngine(scenario)
	if err != nil {
		return SaveStudioResult{}, err
	}
	s.f.scenario = scenario
	s.f.engine = engine
	s.f.workflowRunner = nil // agents / scenario bindings may have changed
	return SaveStudioResult{
		Path:         path,
		ScenarioName: scenario.Name,
		Graph:        graph.MergeLayout(edited, graph.ExportScenario(scenario)),
	}, nil
}

// RunStudioGraph validates an edited graph and executes it as a new run.
func (s Studio) RunStudioGraph(ctx context.Context, edited graph.ScenarioGraph, req RunRequest) (RunResult, error) {
	return s.RunStudioGraphWithScenario(ctx, edited, nil, req)
}

// RunStudioGraphWithScenario executes a graph with an optional additive
// scenario-mode compose draft without mutating the live Framework.
func (s Studio) RunStudioGraphWithScenario(ctx context.Context, edited graph.ScenarioGraph, draft *core.Scenario, req RunRequest) (RunResult, error) {
	scenario, err := resolveStudioScenario(s.f.currentScenario(), edited, draft)
	if err != nil {
		return RunResult{}, err
	}
	if err := ValidateScenario(scenario); err != nil {
		return RunResult{}, err
	}
	ctx, release, err := s.f.acquireRunLease(ctx, &req)
	if err != nil {
		return RunResult{}, err
	}
	defer release()
	var result RunResult
	switch scenario.Orchestration.Mode {
	case core.OrchestrationFixedWorkflow:
		if draft == nil {
			result, err = s.f.runWorkflowScenario(ctx, scenario, req)
		} else {
			engine, buildErr := s.f.buildEphemeralEngine(scenario, nil)
			if buildErr != nil {
				return RunResult{}, buildErr
			}
			runner := s.f.newEphemeralWorkflowRunner(scenario, engine)
			result, err = s.f.runWorkflowScenarioWith(ctx, scenario, req, engine, runner)
			engine.ClearRunScopedState(result.RunID)
		}
	case core.OrchestrationHybrid:
		if draft != nil {
			return RunResult{}, fmt.Errorf("agentflow: Studio scenario drafts support fixed_workflow or autonomous trial runs, not hybrid")
		}
		if scenario.Orchestration.Workflow == nil {
			result, err = s.f.currentEngine().Run(ctx, req)
		} else {
			req, paused, prepErr := s.f.prepareHybridAutonomousRunScenario(ctx, scenario, req)
			if prepErr != nil || paused.Status != "" {
				return paused, mapLeaseLostError(ctx, prepErr)
			}
			result, err = s.f.currentEngine().RunHybrid(ctx, req)
		}
	default:
		engine := s.f.currentEngine()
		if draft != nil {
			engine, err = s.f.buildEphemeralEngine(scenario, nil)
			if err != nil {
				return RunResult{}, err
			}
			defer engine.ClearRunScopedState(req.RunID)
		}
		result, err = engine.Run(ctx, req)
	}
	return result, mapLeaseLostError(ctx, err)
}

func resolveStudioScenario(base core.Scenario, edited graph.ScenarioGraph, draft *core.Scenario) (core.Scenario, error) {
	if draft == nil {
		return graph.ApplyGraph(base, edited)
	}
	patch := graph.ScenarioPatch{
		Agents: make(map[string]core.Agent),
		Skills: make(map[string]core.Skill),
	}
	for name, agent := range draft.Agents {
		if existing, ok := base.Agents[name]; ok {
			if !reflect.DeepEqual(existing, agent) {
				return core.Scenario{}, fmt.Errorf("agentflow: Studio scenario draft modifies existing agent %q", name)
			}
			continue
		}
		patch.Agents[name] = agent
	}
	for name, skill := range draft.Skills {
		if existing, ok := base.Skills[name]; ok {
			if !reflect.DeepEqual(existing, skill) {
				return core.Scenario{}, fmt.Errorf("agentflow: Studio scenario draft modifies existing skill %q", name)
			}
			continue
		}
		patch.Skills[name] = skill
	}
	withParts, err := graph.ApplyScenarioPatch(base, patch)
	if err != nil {
		return core.Scenario{}, err
	}
	return graph.ApplyGraph(withParts, edited)
}

// CompareRuns diffs step outputs between two persisted runs.
func (s Studio) CompareRuns(ctx context.Context, runA, runB string) (studio.RunCompareResult, error) {
	if s.f.runs == nil {
		return studio.RunCompareResult{}, fmt.Errorf("agentflow: run-state repository is not configured")
	}
	if runA == "" || runB == "" {
		return studio.RunCompareResult{}, fmt.Errorf("agentflow: compare requires run_a and run_b")
	}
	snapA, err := runstate.LoadAuthorized(ctx, s.f.runs, runA)
	if err != nil {
		return studio.RunCompareResult{}, err
	}
	snapB, err := runstate.LoadAuthorized(ctx, s.f.runs, runB)
	if err != nil {
		return studio.RunCompareResult{}, err
	}
	return studio.CompareSnapshots(runA, runB, snapA, snapB), nil
}

// ListRunThread returns runs in the same fork/thread group as the given run.
func (s Studio) ListRunThread(ctx context.Context, runID string) ([]ThreadRunSummary, error) {
	if s.f.runs == nil {
		return nil, fmt.Errorf("agentflow: run-state repository is not configured")
	}
	root, err := runstate.LoadAuthorized(ctx, s.f.runs, runID)
	if err != nil {
		return nil, err
	}
	threadID := resolveThreadID(root)
	filter := runstate.ListFilter{ThreadID: threadID, TenantID: root.TenantID}
	if principal, ok := identity.PrincipalFromContext(ctx); ok && principal.Scope.TenantID != "" {
		filter.TenantID = principal.Scope.TenantID
	}
	list, err := s.f.runs.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		list = []runstate.RunSnapshot{root}
	}
	out := make([]ThreadRunSummary, 0, len(list))
	for _, snap := range list {
		if err := runstate.AuthorizeTenant(ctx, snap); err != nil {
			continue
		}
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
func (s Studio) ForkRun(ctx context.Context, parentRunID string, version int64) (ForkRunResult, error) {
	if s.f.runs == nil {
		return ForkRunResult{}, fmt.Errorf("agentflow: run-state repository is not configured")
	}
	parent, err := runstate.LoadAuthorized(ctx, s.f.runs, parentRunID)
	if err != nil {
		return ForkRunResult{}, err
	}
	source := parent
	if version > 0 {
		if s.f.checkpointHistory == nil {
			return ForkRunResult{}, fmt.Errorf("agentflow: checkpoint history is not configured")
		}
		source, err = s.f.checkpointHistory.Load(ctx, parentRunID, version)
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
	if err := s.f.runs.Save(ctx, &child, 0); err != nil {
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

// Thin Framework delegates — prefer Studio() for new code.

func (f *Framework) ValidateStudioGraph(ctx context.Context, edited graph.ScenarioGraph) (ValidateStudioResult, error) {
	return f.Studio().ValidateStudioGraph(ctx, edited)
}

func (f *Framework) ValidateStudioGraphWithScenario(ctx context.Context, edited graph.ScenarioGraph, draft *core.Scenario) (ValidateStudioResult, error) {
	return f.Studio().ValidateStudioGraphWithScenario(ctx, edited, draft)
}

// StudioParts is a thin Framework delegate — prefer Framework.Studio().
func (f *Framework) StudioParts() StudioParts {
	return f.Studio().Parts()
}

func (f *Framework) GenerateStudioBuilderCode(ctx context.Context, edited graph.ScenarioGraph) (CodegenResult, error) {
	return f.Studio().GenerateStudioBuilderCode(ctx, edited)
}

func (f *Framework) GenerateStudioBuilderCodeWithScenario(ctx context.Context, edited graph.ScenarioGraph, draft *core.Scenario) (CodegenResult, error) {
	return f.Studio().GenerateStudioBuilderCodeWithScenario(ctx, edited, draft)
}

func (f *Framework) GenerateStudioScenarioYAML(ctx context.Context, edited graph.ScenarioGraph) (CodegenResult, error) {
	return f.Studio().GenerateStudioScenarioYAML(ctx, edited)
}

func (f *Framework) GenerateStudioScenarioYAMLWithScenario(ctx context.Context, edited graph.ScenarioGraph, draft *core.Scenario) (CodegenResult, error) {
	return f.Studio().GenerateStudioScenarioYAMLWithScenario(ctx, edited, draft)
}

func (f *Framework) ImportStudioScenarioYAML(ctx context.Context, yamlData []byte, layout graph.ScenarioGraph) (ImportStudioResult, error) {
	return f.Studio().ImportStudioScenarioYAML(ctx, yamlData, layout)
}

func (f *Framework) SaveStudioGraph(ctx context.Context, edited graph.ScenarioGraph, path string) (SaveStudioResult, error) {
	return f.Studio().SaveStudioGraph(ctx, edited, path)
}

func (f *Framework) SaveStudioGraphWithScenario(ctx context.Context, edited graph.ScenarioGraph, draft *core.Scenario, path string) (SaveStudioResult, error) {
	return f.Studio().SaveStudioGraphWithScenario(ctx, edited, draft, path)
}

func (f *Framework) RunStudioGraph(ctx context.Context, edited graph.ScenarioGraph, req RunRequest) (RunResult, error) {
	return f.Studio().RunStudioGraph(ctx, edited, req)
}

func (f *Framework) RunStudioGraphWithScenario(ctx context.Context, edited graph.ScenarioGraph, draft *core.Scenario, req RunRequest) (RunResult, error) {
	return f.Studio().RunStudioGraphWithScenario(ctx, edited, draft, req)
}

func (f *Framework) CompareRuns(ctx context.Context, runA, runB string) (studio.RunCompareResult, error) {
	return f.Studio().CompareRuns(ctx, runA, runB)
}

func (f *Framework) ListRunThread(ctx context.Context, runID string) ([]ThreadRunSummary, error) {
	return f.Studio().ListRunThread(ctx, runID)
}

func (f *Framework) ForkRun(ctx context.Context, parentRunID string, version int64) (ForkRunResult, error) {
	return f.Studio().ForkRun(ctx, parentRunID, version)
}
