package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	humancli "github.com/aijustin/agentflow-go/internal/adapter/human/cli"
	"github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

// approvalMigrationScenario builds an autonomous scenario with a pause-gated
// "risky" tool next to the unrestricted "echo" tool.
func approvalMigrationScenario() core.Scenario {
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 8)
	scenario.Tools["risky"] = core.Tool{
		Name:        "risky",
		Type:        "builtin.echo",
		Description: "Risky echo",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Approval:    core.ApprovalPause,
		SideEffect:  core.SideEffectWrite,
	}
	agent := scenario.Agents["assistant"]
	agent.Tools = []string{"echo", "risky"}
	scenario.Agents["assistant"] = agent
	return scenario
}

func queueRiskyTurn(gateway *mock.Gateway, id, query string) {
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: id, Name: "risky", Input: json.RawMessage(`{"q":` + strconvQuote(query) + `}`)}},
	})
}

func strconvQuote(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// TestEngineApprovalCacheSurvivesNodeMigration: a "remembered" approval is
// exported into the next pause checkpoint, so resuming on a fresh engine
// (new node, empty process-local stores) does not re-prompt for it.
func TestEngineApprovalCacheSurvivesNodeMigration(t *testing.T) {
	ctx := context.Background()
	repo := runstateinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("test-secret-012345"))
	if err != nil {
		t.Fatal(err)
	}
	gate := humancli.NewGate(repo, signer, nil)
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	queueRiskyTurn(gateway, "call-1", "a") // pause 1 (approved, remembered)
	queueRiskyTurn(gateway, "call-2", "b") // pause 2 (carries the remembered allow of "a")
	queueRiskyTurn(gateway, "call-3", "a") // must auto-approve on the new node
	queueFinalTurn(gateway, "done")
	scenario := approvalMigrationScenario()
	engine1, err := NewEngine(scenario, Dependencies{
		Runs:          repo,
		LLM:           gateway,
		HumanGate:     gate,
		Tools:         mapToolRegistry{"echo": echoTool{}, "risky": echoTool{}},
		ApprovalStore: toolorch.NewMemoryApprovalStore(),
	})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := engine1.Run(ctx, RunRequest{RunID: "run-mig", Agent: "assistant", Prompt: "go"})
	if err != nil || paused.Status != runstate.RunStatusPaused {
		t.Fatalf("expected pause 1, got %+v err=%v", paused, err)
	}
	if err := gate.Resume(ctx, paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	paused, err = engine1.ContinueAfterCheckpoint(ctx, "run-mig")
	if err != nil || paused.Status != runstate.RunStatusPaused {
		t.Fatalf("expected pause 2, got %+v err=%v", paused, err)
	}
	// The second pause's checkpoint must carry the remembered approval.
	snapshot, err := repo.Load(ctx, "run-mig")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Variables[checkpointApprovalsVar]) == 0 {
		t.Fatal("pause checkpoint must export the approval cache")
	}

	// Node switch: a brand-new engine with an empty approval store resumes
	// from the shared repository.
	engine2, err := NewEngine(scenario, Dependencies{
		Runs:          repo,
		LLM:           gateway,
		HumanGate:     gate,
		Tools:         mapToolRegistry{"echo": echoTool{}, "risky": echoTool{}},
		ApprovalStore: toolorch.NewMemoryApprovalStore(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Resume(ctx, paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	result, err := engine2.ContinueAfterCheckpoint(ctx, "run-mig")
	if err != nil {
		t.Fatal(err)
	}
	// Without the imported allow for {"q":"a"} the third LLM turn would pause
	// again instead of completing.
	if result.Status != runstate.RunStatusCompleted || result.Output != "done" {
		t.Fatalf("expected completion without re-prompting, got %+v", result)
	}
}

// TestEngineDenyBreakerStateSurvivesNodeMigration: the consecutive-deny count
// and the cached deny decision are exported into the pause checkpoint and
// restored on a fresh node, where the streak continues and trips the breaker
// at the configured limit.
func TestEngineDenyBreakerStateSurvivesNodeMigration(t *testing.T) {
	ctx := context.Background()
	repo := runstateinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("test-secret-012345"))
	if err != nil {
		t.Fatal(err)
	}
	gate := humancli.NewGate(repo, signer, nil)
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	scenario := approvalMigrationScenario()
	scenario.Runtime.HITLDenyLimit = 2
	denyInput := json.RawMessage(`{"q":"x"}`)
	store1 := toolorch.NewMemoryApprovalStore()
	// A prior rejection cached the deny (as RememberHITLReject would).
	toolorch.RememberDeny(store1, "run-deny", "risky", denyInput)
	engine1, err := NewEngine(scenario, Dependencies{
		Runs:          repo,
		LLM:           gateway,
		HumanGate:     gate,
		Tools:         mapToolRegistry{"echo": echoTool{}, "risky": echoTool{}},
		ApprovalStore: store1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Turn 1 hits the cached deny (soft denial, breaker count 1); turn 2 is
	// an uncached pause-required call, so the run pauses and the checkpoint
	// exports both the deny cache and the count.
	queueRiskyTurn(gateway, "call-1", "x")
	queueRiskyTurn(gateway, "call-2", "y")
	paused, err := engine1.Run(ctx, RunRequest{RunID: "run-deny", Agent: "assistant", Prompt: "go"})
	if err != nil || paused.Status != runstate.RunStatusPaused {
		t.Fatalf("expected pause, got %+v err=%v", paused, err)
	}
	snapshot, err := repo.Load(ctx, "run-deny")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(snapshot.Variables[checkpointDenyCountVar]); got != "1" {
		t.Fatalf("checkpoint deny count = %q, want 1", got)
	}
	if len(snapshot.Variables[checkpointApprovalsVar]) == 0 {
		t.Fatal("pause checkpoint must export the deny cache")
	}

	// Node switch: restore into a fresh engine and continue the streak.
	engine2, err := NewEngine(scenario, Dependencies{
		Runs:          repo,
		LLM:           gateway,
		HumanGate:     gate,
		Tools:         mapToolRegistry{"echo": echoTool{}, "risky": echoTool{}},
		ApprovalStore: toolorch.NewMemoryApprovalStore(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := engine2.tooling.denyBreaker.ExportRun("run-deny"); got != 0 {
		t.Fatalf("fresh node must start at 0, got %d", got)
	}
	engine2.restoreApprovalCheckpointState(ctx, "run-deny", snapshot.Variables)
	if got := engine2.tooling.denyBreaker.ExportRun("run-deny"); got != 1 {
		t.Fatalf("restored deny count = %d, want 1", got)
	}
	if decision, ok := engine2.tooling.approvalStore.Get("run-deny", toolorch.Key("risky", denyInput)); !ok || decision != toolorch.DecisionDeny {
		t.Fatalf("cached deny not restored: %q, %v", decision, ok)
	}
	// One more consecutive denial trips the breaker at limit 2.
	err = engine2.noteApprovalDeny(ctx, "run-deny", "risky")
	if err == nil || !strings.Contains(err.Error(), "deny breaker") {
		t.Fatalf("expected breaker trip, got %v", err)
	}
}
