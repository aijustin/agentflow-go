package agentflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
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
	repo := agentflow.NewInMemoryRunStateRepository()
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
	queue := agentflow.NewInMemoryJobQueue()
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
