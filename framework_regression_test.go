package agentflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/adapters"

	"github.com/aijustin/agentflow-go"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestFrameworkRunStructuredHydratesWorkflowContext(t *testing.T) {
	scenario := core.Scenario{
		Name: "structured-hydrate",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {
				Name: "assistant",
				LLM:  "default",
				Policy: core.AgentPolicy{
					OutputSchema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`),
				},
			},
		},
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Approval: core.ApprovalNever},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "prep", Kind: core.NodeTool, Ref: "echo", Input: json.RawMessage(`{"message":"workflow-prep"}`)},
				},
			},
		},
	}
	gateway := &contextCapturingStructuredGateway{payload: json.RawMessage(`{"answer":"ok"}`)}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("echo", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fw.RunStructured(context.Background(), agentflow.RunRequest{
		RunID:  "run-hydrate",
		Agent:  "assistant",
		Prompt: "summarize workflow output",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !gateway.sawWorkflowPrep {
		t.Fatalf("structured phase should receive hydrated workflow context, messages=%+v", gateway.lastMessages)
	}
}

type contextCapturingStructuredGateway struct {
	payload         json.RawMessage
	lastMessages    []llm.Message
	sawWorkflowPrep bool
}

func (g *contextCapturingStructuredGateway) Supports(string, llm.Capability) bool { return true }

func (g *contextCapturingStructuredGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{Message: llm.Message{Content: string(g.payload)}}, nil
}

func (g *contextCapturingStructuredGateway) StructuredChat(_ context.Context, _ string, _ json.RawMessage, req llm.ChatRequest) (json.RawMessage, error) {
	g.lastMessages = append([]llm.Message(nil), req.Messages...)
	for _, msg := range req.Messages {
		if strings.Contains(msg.Content, "workflow-prep") || strings.Contains(msg.Content, `"prep"`) {
			g.sawWorkflowPrep = true
		}
	}
	return g.payload, nil
}

func TestFrameworkJobHandlerRunReturnsPausedError(t *testing.T) {
	repo := adapters.NewInMemoryRunStateRepository()
	fw, err := agentflow.New(
		builder.MinimalHumanInLoop("assistant"),
		agentflow.WithRunStateRepository(repo),
		agentflow.WithHumanGate(&frameworkTestGate{repo: repo}),
		agentflow.WithLLMGateway(fakeGateway{content: "unused"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(asyncpkg.RunPayload{
		RunID:  "run-async-pause",
		Agent:  "assistant",
		Prompt: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = handler.HandleJob(context.Background(), asyncpkg.Job{
		ID:      "job-pause",
		Type:    asyncpkg.RunJobType,
		RunID:   "run-async-pause",
		Payload: payload,
	})
	var paused asyncpkg.RunPausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected RunPausedError, got %v", err)
	}
	if paused.RunID != "run-async-pause" || paused.Token == "" {
		t.Fatalf("unexpected pause payload: %+v", paused)
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), "run-async-pause")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused run snapshot, got %s", snapshot.Status)
	}
}

func TestAsyncWorkerPauseJob(t *testing.T) {
	queue := adapters.NewInMemoryJobQueue()
	job, err := queue.Enqueue(context.Background(), asyncpkg.Job{
		ID:          "job-pause-queue",
		Type:        asyncpkg.RunJobType,
		MaxAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, ok, err := queue.Lease(context.Background(), "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("lease: ok=%v err=%v", ok, err)
	}
	if err := queue.Pause(context.Background(), lease, asyncpkg.PauseResult{RunID: "run-1", Token: "tok-123"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := queue.Load(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != asyncpkg.JobPaused {
		t.Fatalf("expected job paused, got %s", loaded.State)
	}
	if !strings.Contains(loaded.LastError, "tok-123") {
		t.Fatalf("expected token persisted, got %q", loaded.LastError)
	}
}

type frameworkTestGate struct {
	repo runstate.Repository
}

func (g *frameworkTestGate) Pause(ctx context.Context, state core.CheckpointState) (string, error) {
	snapshot, err := runstate.LoadAuthorized(ctx, g.repo, state.RunID)
	if err != nil {
		return "", err
	}
	snapshot.Status = runstate.RunStatusPaused
	snapshot.PendingGate = &state
	if err := g.repo.Save(ctx, &snapshot, snapshot.Version); err != nil {
		return "", err
	}
	return "pause-token", nil
}

func (g *frameworkTestGate) Resume(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) error {
	return nil
}

// TestFrameworkMaxStepsTerminalPayloadReason pins the C1 contract at the
// facade level: a run that exhausts its step budget fails with a terminal
// payload whose termination_reason is max_steps_exceeded.
func TestFrameworkMaxStepsTerminalPayloadReason(t *testing.T) {
	scenario := core.Scenario{
		Name: "maxsteps-reason",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {
				Name:   "assistant",
				LLM:    "default",
				Tools:  []string{"echo"},
				Policy: core.AgentPolicy{MaxSteps: 1},
			},
		},
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Approval: core.ApprovalNever},
		},
	}
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Input: json.RawMessage(`{}`)}},
	})
	sink := &terminalEventSink{}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithEventSink(sink),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-maxsteps", Agent: "assistant", Prompt: "loop"})
	if err == nil || !strings.Contains(err.Error(), "max_steps=1") {
		t.Fatalf("expected max_steps error, got %v", err)
	}
	payload := sink.terminalPayload(t, core.EventRunFailed)
	if payload.TerminationReason != core.TerminationReasonMaxStepsExceeded {
		t.Fatalf("TerminationReason=%q want %q", payload.TerminationReason, core.TerminationReasonMaxStepsExceeded)
	}
}

// TestFrameworkCancelTerminalPayloadReason pins the C1 contract for
// cancellation: the RunCancelled event carries a structured payload with
// termination_reason=cancelled instead of a nil payload.
func TestFrameworkCancelTerminalPayloadReason(t *testing.T) {
	scenario := core.Scenario{
		Name:   "cancel-reason",
		LLMs:   map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{"assistant": {Name: "assistant", LLM: "default"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	sink := &terminalEventSink{}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(&cancelOnChatGateway{cancel: cancel}),
		agentflow.WithEventSink(sink),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fw.Run(ctx, agentflow.RunRequest{RunID: "run-cancel-reason", Agent: "assistant", Prompt: "hi"})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	payload := sink.terminalPayload(t, core.EventRunCancelled)
	if payload.Status != "cancelled" || payload.TerminationReason != core.TerminationReasonCancelled {
		t.Fatalf("unexpected cancelled payload: %+v", payload)
	}
}

// terminalEventSink records events for terminal payload assertions.
type terminalEventSink struct {
	mu     sync.Mutex
	events []core.Event
}

func (s *terminalEventSink) Emit(_ context.Context, event core.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *terminalEventSink) terminalPayload(t *testing.T, typ core.EventType) core.RunTerminalPayload {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range s.events {
		if event.Type != typ {
			continue
		}
		var payload core.RunTerminalPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("decode %s payload: %v (raw=%s)", typ, err, event.Payload)
		}
		return payload
	}
	t.Fatalf("expected %s event", typ)
	return core.RunTerminalPayload{}
}

// cancelOnChatGateway cancels the caller's context mid-call, simulating a
// user abort arriving while the provider request is in flight.
type cancelOnChatGateway struct {
	cancel context.CancelFunc
}

func (g *cancelOnChatGateway) Supports(string, llm.Capability) bool { return true }

func (g *cancelOnChatGateway) Chat(ctx context.Context, _ string, _ llm.ChatRequest) (llm.ChatResponse, error) {
	g.cancel()
	return llm.ChatResponse{}, ctx.Err()
}
