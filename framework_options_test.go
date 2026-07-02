package agentflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

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
	memRepo, err := agentflow.NewFileMemoryRepository(t.TempDir())
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
	queue := agentflow.NewInMemoryJobQueue()
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
