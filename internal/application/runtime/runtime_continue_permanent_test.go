package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func seedContinueCheckpoint(t *testing.T, repo runstate.Repository, runID string, vars map[string]json.RawMessage) {
	t.Helper()
	base := map[string]json.RawMessage{
		"checkpoint_kind":   json.RawMessage(`"before_final_answer"`),
		"checkpoint_prompt": json.RawMessage(`"finish the answer"`),
		"checkpoint_agent":  json.RawMessage(`"assistant"`),
	}
	for k, v := range vars {
		base[k] = v
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: runID, ScenarioName: "scenario", Status: runstate.RunStatusRunning, Variables: base,
	}, 0); err != nil {
		t.Fatal(err)
	}
}

func loadRun(t *testing.T, repo runstate.Repository, runID string) runstate.RunSnapshot {
	t.Helper()
	snapshot, err := runstate.LoadAuthorized(context.Background(), repo, runID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// TestContinueBeforeFinalGatewayMissingMarksFailed: resume approve with no
// LLM gateway wired is a permanent configuration error — the run must reach
// Failed (not linger in Running), keep its checkpoint variables, and become
// continuable once a gateway is attached.
func TestContinueBeforeFinalGatewayMissingMarksFailed(t *testing.T) {
	repo := runstateinmem.NewRepository()
	seedContinueCheckpoint(t, repo, "run-no-gw", nil)
	engine, err := NewEngine(baseScenario(false), Dependencies{
		Runs: repo,
		LLM:  nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.ContinueAfterCheckpoint(context.Background(), "run-no-gw")
	if err == nil || !strings.Contains(err.Error(), "llm gateway is required") {
		t.Fatalf("expected gateway-required error, got %v", err)
	}
	snapshot := loadRun(t, repo, "run-no-gw")
	if snapshot.Status != runstate.RunStatusFailed {
		t.Fatalf("expected Failed after permanent continue error, got %s", snapshot.Status)
	}
	if got := variableString(snapshot.Variables, "run_error_message"); !strings.Contains(got, "llm gateway is required") {
		t.Fatalf("expected gateway reason on snapshot, got %q", got)
	}
	if got := variableString(snapshot.Variables, "checkpoint_kind"); got != "before_final_answer" {
		t.Fatalf("checkpoint variables must be kept for recovery, got %q", got)
	}
	// A Failed run no longer continues directly; the recovery entry point is
	// RetryFailedRun (covered end-to-end at the facade level).
	if _, err := engine.ContinueAfterCheckpoint(context.Background(), "run-no-gw"); err == nil ||
		!strings.Contains(err.Error(), "requires running snapshot") {
		t.Fatalf("expected continue-after-failed rejection, got %v", err)
	}
}

// rateLimitedGateway fails every chat call with a retryable 429 APIError.
type rateLimitedGateway struct{ calls int }

func (g *rateLimitedGateway) Supports(string, llm.Capability) bool { return true }

func (g *rateLimitedGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	g.calls++
	return llm.ChatResponse{}, llm.APIError{Provider: "mock", StatusCode: 429, Status: "429", Body: "slow down"}
}

// TestContinueBeforeFinalTransientErrorKeepsRunning: a retryable provider
// error keeps the existing transient semantics — the run stays Running with
// its checkpoint intact for a later ContinueRun.
func TestContinueBeforeFinalTransientErrorKeepsRunning(t *testing.T) {
	repo := runstateinmem.NewRepository()
	seedContinueCheckpoint(t, repo, "run-429", nil)
	gateway := &rateLimitedGateway{}
	engine, err := NewEngine(baseScenario(false), Dependencies{
		Runs: repo,
		LLM:  gateway,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.ContinueAfterCheckpoint(context.Background(), "run-429")
	if err == nil {
		t.Fatal("expected the provider error to surface")
	}
	snapshot := loadRun(t, repo, "run-429")
	if snapshot.Status != runstate.RunStatusRunning {
		t.Fatalf("transient error must keep the run Running, got %s", snapshot.Status)
	}
	if got := variableString(snapshot.Variables, "checkpoint_kind"); got != "before_final_answer" {
		t.Fatalf("checkpoint must stay intact, got %q", got)
	}
}

// TestContinueToolApprovalValidationMarksFailed: a tool-approval continue
// whose profile cannot call tools fails validation — a permanent error that
// must mark the run Failed instead of leaving it Running.
func TestContinueToolApprovalValidationMarksFailed(t *testing.T) {
	repo := runstateinmem.NewRepository()
	seedContinueCheckpoint(t, repo, "run-no-toolcap", map[string]json.RawMessage{
		"checkpoint_kind":       json.RawMessage(`"tool_approval"`),
		"checkpoint_tool_calls": json.RawMessage(`[{"id":"c1","name":"echo","input":{}}]`),
		"checkpoint_messages":   json.RawMessage(`[{"role":"user","content":"hi"}]`),
	})
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat) // no CapToolCall
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs:  repo,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.ContinueAfterCheckpoint(context.Background(), "run-no-toolcap")
	if err == nil || !strings.Contains(err.Error(), "does not support tool calling") {
		t.Fatalf("expected tool-calling validation error, got %v", err)
	}
	snapshot := loadRun(t, repo, "run-no-toolcap")
	if snapshot.Status != runstate.RunStatusFailed {
		t.Fatalf("expected Failed after permanent validation error, got %s", snapshot.Status)
	}
	if got := variableString(snapshot.Variables, "checkpoint_kind"); got != "tool_approval" {
		t.Fatalf("checkpoint must stay intact for RetryFailedRun, got %q", got)
	}
}
