package agentflow

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aijustin/agentflow-go/internal/application/orchestration"
	appexec "github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/graph"
)

// ComposeMode selects what the AI composer may generate.
type ComposeMode string

const (
	// ComposeModeCatalog (default) lets the composer orchestrate only parts
	// already registered in the base scenario (agents, tools, skills,
	// subgraphs): it produces topology, nothing else.
	ComposeModeCatalog ComposeMode = "catalog"
	// ComposeModeScenario additionally lets the composer create new agents
	// and prompt skills, merged as an additive patch. Overwriting existing
	// part IDs is rejected, and new tools are never executable unless the
	// host bound an executor/resolver for them.
	ComposeModeScenario ComposeMode = "scenario"
)

// ComposeGraphRequest drives one AI graph-composition call.
type ComposeGraphRequest struct {
	// Prompt is the one-sentence task the composed graph should accomplish.
	Prompt string `json:"prompt"`
	// Mode selects catalog (default) or scenario composition.
	Mode ComposeMode `json:"mode,omitempty"`
	// ComposerLLM names the LLM profile the composer agent uses; empty
	// resolves to the scenario's "default" profile or the first profile.
	ComposerLLM string `json:"composer_llm,omitempty"`
	// MaxSteps caps the composer tool loop. Default 15.
	MaxSteps int `json:"max_steps,omitempty"`
	// Run executes the composed graph after validation when true. Runs are
	// ephemeral: the live framework scenario is never replaced.
	Run bool `json:"run,omitempty"`
	// RunRequest is passed through to the run when Run is true (run_id,
	// prompt, context, trust mode).
	RunRequest RunRequest `json:"run_request,omitempty"`
}

// ComposeGraphResult reports the outcome of one composition call.
type ComposeGraphResult struct {
	Mode ComposeMode `json:"mode"`
	// Graph is the exported topology of the composed (merged) scenario.
	Graph graph.ScenarioGraph `json:"graph"`
	// Scenario is the merged temporary scenario. It is never installed as
	// the live scenario; persist explicitly via SaveStudioGraph or YAML
	// codegen if desired.
	Scenario *core.Scenario `json:"scenario,omitempty"`
	Valid    bool           `json:"valid"`
	Error    string         `json:"error,omitempty"`
	// Run holds the ephemeral run outcome when Run was requested and the
	// composed graph validated.
	Run *RunResult `json:"run,omitempty"`
}

// composeAgentName is the fixed name of the internal composer agent.
const composeAgentName = "graph_composer"

const composeDefaultMaxSteps = 15

// ComposeGraph generates a validated scenario graph for a natural-language
// task using an agentic composer: an internal agent builds the draft through
// compose_* tools with incremental validation feedback, the result is merged
// onto a deep copy of the live scenario, fully validated, and optionally
// executed ephemerally. The live framework state is never mutated.
func (s Studio) ComposeGraph(ctx context.Context, req ComposeGraphRequest) (ComposeGraphResult, error) {
	mode := req.Mode
	if mode == "" {
		mode = ComposeModeCatalog
	}
	if mode != ComposeModeCatalog && mode != ComposeModeScenario {
		return ComposeGraphResult{}, fmt.Errorf("agentflow: compose mode %q is unsupported (want %q or %q)", req.Mode, ComposeModeCatalog, ComposeModeScenario)
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return ComposeGraphResult{}, fmt.Errorf("agentflow: compose prompt is required")
	}
	base := s.f.currentScenario()
	defaultLLM, err := resolveComposerLLM(base, req.ComposerLLM)
	if err != nil {
		return ComposeGraphResult{}, err
	}
	builder := newComposeGraphBuilder(base, mode, defaultLLM)
	composerScenario := buildComposerScenario(base, mode, defaultLLM, req.MaxSteps)
	registry := composeToolRegistry{base: s.f.tools, extra: builder.toolExecutors()}
	engine, err := s.f.buildEphemeralEngine(composerScenario, registry)
	if err != nil {
		return ComposeGraphResult{}, fmt.Errorf("agentflow: compose engine: %w", err)
	}
	composerRunID := req.RunRequest.RunID
	if composerRunID == "" {
		composerRunID = generateRunID()
	} else {
		composerRunID += "-compose"
	}
	fail := func(err error) (ComposeGraphResult, error) {
		return ComposeGraphResult{Mode: mode, Valid: false, Error: err.Error()}, nil
	}
	if _, err := engine.Run(ctx, RunRequest{
		RunID:  composerRunID,
		Agent:  composeAgentName,
		Prompt: req.Prompt,
	}); err != nil {
		return fail(fmt.Errorf("composer run failed: %w", err))
	}
	if !builder.finished() {
		return fail(fmt.Errorf("composer did not call %s; the draft is incomplete", composeToolFinish))
	}
	applied, err := builder.appliedScenario()
	if err != nil {
		return fail(err)
	}
	if err := ValidateScenario(applied); err != nil {
		return fail(err)
	}
	exported := graph.ExportScenario(applied)
	result := ComposeGraphResult{
		Mode:     mode,
		Graph:    exported,
		Scenario: &applied,
		Valid:    true,
	}
	if !req.Run {
		return result, nil
	}
	runResult, err := s.runComposedGraph(ctx, mode, builder, applied, req.RunRequest)
	if err != nil {
		return ComposeGraphResult{}, err
	}
	result.Run = &runResult
	return result, nil
}

// runComposedGraph executes a validated composed graph ephemerally.
func (s Studio) runComposedGraph(ctx context.Context, mode ComposeMode, builder *composeGraphBuilder, applied core.Scenario, req RunRequest) (RunResult, error) {
	if mode == ComposeModeCatalog {
		// Catalog graphs only rewire topology over the live parts, so the
		// existing Studio run path (which supports hybrid via the live
		// engine) is correct.
		return s.RunStudioGraph(ctx, builder.scenarioGraph(), req)
	}
	if applied.Orchestration.Mode != core.OrchestrationFixedWorkflow {
		return RunResult{}, fmt.Errorf("agentflow: compose run for scenario mode supports %q scenarios only, got %q", core.OrchestrationFixedWorkflow, applied.Orchestration.Mode)
	}
	engine, err := s.f.buildEphemeralEngine(applied, nil)
	if err != nil {
		return RunResult{}, fmt.Errorf("agentflow: compose run engine: %w", err)
	}
	runner := s.f.newEphemeralWorkflowRunner(applied, engine)
	ctx, release, err := s.f.acquireRunLease(ctx, &req)
	if err != nil {
		return RunResult{}, err
	}
	defer release()
	result, err := s.f.runWorkflowScenarioWith(ctx, applied, req, engine, runner)
	engine.ClearRunScopedState(result.RunID)
	return result, mapLeaseLostError(ctx, err)
}

// newEphemeralWorkflowRunner builds an uncached runner bound to a temporary
// scenario/engine pair (unlike newWorkflowRunner, which caches the live pair).
func (f *Framework) newEphemeralWorkflowRunner(scenario core.Scenario, engine *appexec.Engine) *orchestration.WorkflowRunner {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return orchestration.NewWorkflowRunner(
		f.tools,
		f.runs,
		teeEventSink{inner: f.events},
		orchestration.WithAgentRegistry(workflowAgentRegistry{agents: scenario.Agents, engine: engine}),
		orchestration.WithHumanGate(f.gate),
		orchestration.WithToolApprovalEvaluator(f.approvalEvaluator),
		orchestration.WithBlobStore(f.blobs),
		orchestration.WithSecurityPolicy(f.policy),
		orchestration.WithAuditSink(f.audit),
		orchestration.WithWorkflowToolPolicy(f.toolGov),
		orchestration.WithOutputRedactor(f.redactor),
		orchestration.WithMemoryRewinder(engine),
	)
}

// composeToolRegistry resolves per-call compose tools first, then falls back
// to the framework registry so the composer could (in principle) inspect
// host tools without gaining access to executors it was not given.
type composeToolRegistry struct {
	base  *toolRegistry
	extra map[string]core.ToolExecutor
}

func (r composeToolRegistry) ResolveTool(ctx context.Context, tool core.Tool) (core.ToolExecutor, bool, error) {
	if executor, ok := r.extra[tool.Name]; ok {
		return executor, true, nil
	}
	return r.base.ResolveTool(ctx, tool)
}

// resolveComposerLLM picks the LLM profile for the composer agent.
func resolveComposerLLM(base core.Scenario, requested string) (string, error) {
	if requested != "" {
		if _, ok := base.LLMs[requested]; !ok {
			return "", fmt.Errorf("agentflow: composer llm %q not found in scenario", requested)
		}
		return requested, nil
	}
	if len(base.LLMs) == 0 {
		return "", fmt.Errorf("agentflow: compose requires at least one llm profile in the scenario")
	}
	if _, ok := base.LLMs["default"]; ok {
		return "default", nil
	}
	names := make([]string, 0, len(base.LLMs))
	for name := range base.LLMs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names[0], nil
}

// buildComposerScenario assembles the temporary autonomous scenario the
// composer agent runs in: the compose tool set plus a single agent bound to
// the host's LLM profiles. It exists only for the duration of one
// ComposeGraph call.
func buildComposerScenario(base core.Scenario, mode ComposeMode, defaultLLM string, maxSteps int) core.Scenario {
	if maxSteps <= 0 {
		maxSteps = composeDefaultMaxSteps
	}
	manifests := composeToolManifests(mode)
	toolNames := make([]string, 0, len(manifests))
	for name := range manifests {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)
	return core.Scenario{
		Name:  base.Name + "-compose",
		LLMs:  base.LLMs,
		Tools: manifests,
		Agents: map[string]core.Agent{
			composeAgentName: {
				Name:         composeAgentName,
				Description:  "AI graph composer",
				Instructions: composeInstructions(mode),
				LLM:          defaultLLM,
				Tools:        toolNames,
				Policy: core.AgentPolicy{
					MaxSteps: maxSteps,
				},
				CompletionRequirement: &core.CompletionRequirement{
					Tool:     composeToolFinish,
					Reminder: "You have not finalized the graph. Call compose_validate to check the draft, fix any error, then call compose_finish.",
				},
			},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
}

func composeInstructions(mode ComposeMode) string {
	var b strings.Builder
	b.WriteString(`You are a workflow graph composer for the agentflow runtime. Build a workflow DAG that accomplishes the user's task by calling the compose_* tools.

Procedure:
1. Discover available parts with compose_list_parts (agents, tools, skills, subgraphs).
2. Add nodes with compose_add_node and connect them with compose_connect. Use compose_set_input for node specs (e.g. transform {"set":{...}}, loop {"body":[...]}).
3. Verify the draft with compose_validate and fix every reported error.
4. Finalize with compose_finish.

Rules:
- Allowed node kinds: agent, tool, skill, transform, human_gate, parallel_group, loop, subgraph.
- agent/tool/skill/subgraph nodes must reference an existing part by name; unknown references are rejected — read the error, list parts, and correct yourself.
- Edge conditions may use exists(...), missing(...), eq(...), ne(...) over steps.<node_id>... paths.
- The graph must be a connected DAG with at least one executable path from a root node.
`)
	if mode == ComposeModeScenario {
		b.WriteString(`- You may create NEW agents (compose_add_agent) and prompt skills (compose_add_skill) when no existing part fits. New agents may bind existing tools and skills (or skills you just added). You cannot create new executable tools; only tools listed by compose_list_parts can run.
`)
	} else {
		b.WriteString(`- Catalog mode: you may only orchestrate existing parts. Creating new agents or skills is not allowed.
`)
	}
	return b.String()
}

// ComposeGraph is a thin Framework delegate — prefer Framework.Studio().
func (f *Framework) ComposeGraph(ctx context.Context, req ComposeGraphRequest) (ComposeGraphResult, error) {
	return f.Studio().ComposeGraph(ctx, req)
}
