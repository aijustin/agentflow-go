package agentflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	tierinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/inmem"
	"github.com/aijustin/agentflow-go/pkg/adapters"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/interjection"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

type allowAllApprovals struct{}

func (allowAllApprovals) PauseRequired(context.Context, string, core.Tool, llm.ToolCall) (bool, error) {
	return false, nil
}

func TestBuildPlan(t *testing.T) {
	scenario := testAutonomousScenario()
	plan, err := agentflow.BuildPlan(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Scenario.Name != scenario.Name {
		t.Fatalf("unexpected scenario name: %s", plan.Scenario.Name)
	}
	if len(plan.LLMs) == 0 {
		t.Fatal("expected resolved LLM metadata")
	}
}

func TestBuildPlanRejectsInvalidScenario(t *testing.T) {
	_, err := agentflow.BuildPlan(core.Scenario{Name: "invalid"})
	if err == nil {
		t.Fatal("expected validation error for scenario without agents")
	}
}

func TestFrameworkGetters(t *testing.T) {
	scenario := testAutonomousScenario()
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(fakeGateway{content: "ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if fw.Scenario().Name != scenario.Name {
		t.Fatalf("unexpected scenario: %+v", fw.Scenario())
	}
	if len(fw.Catalog().Agents) == 0 {
		t.Fatalf("expected catalog agents: %+v", fw.Catalog())
	}
	if fw.RunStateRepository() == nil || fw.BlobStore() == nil {
		t.Fatal("expected default repositories")
	}
}

func TestFrameworkClassifyExistingRunStates(t *testing.T) {
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	repo := fw.RunStateRepository()
	cases := []struct {
		name   string
		runID  string
		status runstate.RunStatus
		want   error
	}{
		{name: "paused", runID: "run-paused-dup", status: runstate.RunStatusPaused, want: agentflow.ErrRunPaused},
		{name: "failed", runID: "run-failed-dup", status: runstate.RunStatusFailed, want: agentflow.ErrRunFailed},
		{name: "running", runID: "run-running-dup", status: runstate.RunStatusRunning, want: agentflow.ErrRunInProgress},
		{name: "cancelled", runID: "run-cancelled-dup", status: runstate.RunStatusCancelled, want: agentflow.ErrRunCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := runstate.RunSnapshot{
				RunID:        tc.runID,
				ScenarioName: "wf-retry",
				Status:       tc.status,
			}
			if err := repo.Save(context.Background(), &snapshot, 0); err != nil {
				t.Fatal(err)
			}
			_, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: tc.runID, Prompt: "go"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestFrameworkWiringOptionsSmoke(t *testing.T) {
	scenario := testAutonomousScenario()
	var emitted bool
	memRepo, err := adapters.NewFileMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(fakeGateway{content: "ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithEventSink(core.EventSinkFunc(func(context.Context, core.Event) error {
			emitted = true
			return nil
		})),
		agentflow.WithAuditSink(audit.SinkFunc(func(context.Context, audit.Event) error { return nil })),
		agentflow.WithToolGovernancePolicy(governance.ToolPolicyFunc(func(context.Context, governance.ToolInvocation) error {
			return nil
		})),
		agentflow.WithOutputRedactor(governance.OutputRedactorFunc(func(_ context.Context, redaction governance.OutputRedaction) (json.RawMessage, error) {
			return redaction.Data, nil
		})),
		agentflow.WithLogger(discardLogger{}),
		agentflow.WithRecorder(observability.NoopRecorder{}),
		agentflow.WithMemoryRepository("session", memRepo),
		agentflow.WithLLMPayloadCapture(true),
		agentflow.WithToolApprovalEvaluator(allowAllApprovals{}),
		agentflow.WithToolOutputTransform("echo", func(_ string, raw []byte, _ int) ([]byte, contextwindow.TransformMeta) {
			return raw, contextwindow.TransformMeta{}
		}),
		agentflow.WithInterjectDrainPolicy(interjection.DrainPolicy{}),
		agentflow.WithDatabase(nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-options", Agent: "assistant", Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	if !emitted {
		t.Fatal("expected event sink to receive run events")
	}
	if err := fw.Interject("run-options", "steer"); err == nil {
		t.Fatal("expected interject to reject terminal/missing run")
	}
	if err := fw.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFrameworkWithTierColdSummaryIndexer(t *testing.T) {
	indexer := stubColdSummaryIndexer{}
	fw, err := agentflow.New(
		testAutonomousScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithTierColdSummaryIndexer("session", indexer),
	)
	if err != nil {
		t.Fatal(err)
	}
	if fw == nil {
		t.Fatal("expected framework with cold summary indexer")
	}
	if _, err := agentflow.New(testAutonomousScenario(), agentflow.WithTierColdSummaryIndexer("", indexer)); err == nil {
		t.Fatal("expected empty name error")
	}
	if _, err := agentflow.New(testAutonomousScenario(), agentflow.WithTierColdSummaryIndexer("session", nil)); err == nil {
		t.Fatal("expected nil indexer error")
	}
}

type stubColdSummaryIndexer struct{}

func (stubColdSummaryIndexer) UpsertSummary(context.Context, memory.Namespace, tier.Record, string) error {
	return nil
}

func (stubColdSummaryIndexer) SearchSummaries(context.Context, memory.Namespace, string, int) ([]string, error) {
	return nil, nil
}

func (stubColdSummaryIndexer) DeleteSummary(context.Context, memory.Namespace, string) error {
	return nil
}

func TestFrameworkResumeRequiresGate(t *testing.T) {
	fw, err := agentflow.New(
		testAutonomousScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fw.Resume(context.Background(), "token", core.DecisionApprove, nil); err == nil {
		t.Fatal("expected human gate required error")
	}
}

func TestFrameworkWithJobQueueOption(t *testing.T) {
	queue := adapters.NewInMemoryJobQueue()
	fw, err := agentflow.New(
		testAutonomousScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithJobQueue(queue),
	)
	if err != nil {
		t.Fatal(err)
	}
	if fw == nil {
		t.Fatal("expected framework with job queue")
	}
}

type discardLogger struct{}

func (discardLogger) Warn(context.Context, string, ...any)  {}
func (discardLogger) Error(context.Context, string, ...any) {}

func TestFrameworkWiringOptionsRejectNil(t *testing.T) {
	scenario := testAutonomousScenario()
	cases := []struct {
		name string
		opt  agentflow.Option
	}{
		{name: "event sink", opt: agentflow.WithEventSink(nil)},
		{name: "tool governance", opt: agentflow.WithToolGovernancePolicy(nil)},
		{name: "output redactor", opt: agentflow.WithOutputRedactor(nil)},
		{name: "logger", opt: agentflow.WithLogger(nil)},
		{name: "recorder", opt: agentflow.WithRecorder(nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := agentflow.New(scenario, tc.opt); err == nil {
				t.Fatal("expected nil option error")
			}
		})
	}
}

func TestFrameworkWithMemoryAndTierOptionsRejectInvalid(t *testing.T) {
	scenario := testAutonomousScenario()
	memRepo, err := adapters.NewFileMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentflow.New(scenario, agentflow.WithMemoryRepository("", memRepo)); err == nil {
		t.Fatal("expected empty memory name error")
	}
	if _, err := agentflow.New(scenario, agentflow.WithMemoryRepository("session", nil)); err == nil {
		t.Fatal("expected nil memory repo error")
	}
	if _, err := agentflow.New(scenario, agentflow.WithTierMemory("", tier.NewManager(tierinmem.NewStore(), tier.DefaultPolicy(), tier.NoopMigrationObserver{}))); err == nil {
		t.Fatal("expected empty tier memory name error")
	}
}

func TestCodexPortFrameworkOptions(t *testing.T) {
	scenario := testAutonomousScenario()
	store := toolorch.NewMemoryApprovalStore()
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(fakeGateway{content: "ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithApprovalStore(store),
		agentflow.WithToolOrchestrator(toolorch.NewStoreOrchestrator(store)),
		agentflow.WithInterjectDrainPolicy(interjection.DrainPolicy{
			BeforeSample:          true,
			AfterToolBatch:        true,
			DeferUntilPostCompact: true,
		}),
		agentflow.WithTurnStopHook(func(context.Context, core.TurnStopInfo) (core.TurnStopDecision, error) {
			return core.TurnStopDecision{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-codex-opts", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCodexPortFrameworkOptionsRejectNil(t *testing.T) {
	scenario := testAutonomousScenario()
	if _, err := agentflow.New(scenario, agentflow.WithApprovalStore(nil)); err == nil {
		t.Fatal("expected nil approval store error")
	}
	if _, err := agentflow.New(scenario, agentflow.WithToolOrchestrator(nil)); err == nil {
		t.Fatal("expected nil orchestrator error")
	}
	if _, err := agentflow.New(scenario, agentflow.WithTurnStopHook(nil)); err == nil {
		t.Fatal("expected nil turn stop hook error")
	}
}

func TestFrameworkToolResolverRejectsNilExecutor(t *testing.T) {
	scenario := core.Scenario{
		Name: "nil-resolver",
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Approval: core.ApprovalNever},
		},
		Agents: map[string]core.Agent{"worker": {Name: "worker"}},
		Orchestration: core.Orchestration{
			Mode:     core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{{ID: "echo", Kind: core.NodeTool, Ref: "echo"}}},
		},
	}
	fw, err := agentflow.New(scenario, agentflow.WithToolResolver(adapters.ToolResolverFunc(func(context.Context, core.Tool) (core.ToolExecutor, error) {
		return nil, nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-nil-tool", Agent: "worker"}); err == nil {
		t.Fatal("expected nil executor error")
	}
}
