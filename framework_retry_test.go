package agentflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type retryToolFunc func(context.Context, core.ToolCall) (core.ToolResult, error)

func (f retryToolFunc) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	return f(ctx, call)
}

// flakyGateway fails the first N Chat calls, then answers with content.
type flakyGateway struct {
	mu       sync.Mutex
	failures int
	content  string
}

func (g *flakyGateway) Supports(string, llm.Capability) bool { return true }

func (g *flakyGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failures > 0 {
		g.failures--
		return llm.ChatResponse{}, fmt.Errorf("llm unavailable")
	}
	return llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: g.content}}, nil
}

func retryWorkflowScenario() core.Scenario {
	return core.Scenario{
		Name: "wf-retry",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Tools: map[string]core.Tool{
			"stepA": {Name: "stepA", Type: "builtin.echo"},
			"stepB": {Name: "stepB", Type: "builtin.echo"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTool, Ref: "stepA", Input: json.RawMessage(`{}`)},
					{ID: "b", Kind: core.NodeTool, Ref: "stepB", DependsOn: []string{"a"}, Input: json.RawMessage(`{}`)},
				},
			},
		},
	}
}

func TestFrameworkRetryFailedWorkflowRunSkipsDoneNodes(t *testing.T) {
	var mu sync.Mutex
	aCalls, bCalls := 0, 0
	bShouldFail := true
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", retryToolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
			mu.Lock()
			defer mu.Unlock()
			aCalls++
			return core.ToolResult{Tool: "stepA", Output: json.RawMessage(`{"ok":true}`)}, nil
		})),
		agentflow.WithToolExecutor("stepB", retryToolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
			mu.Lock()
			defer mu.Unlock()
			bCalls++
			if bShouldFail {
				return core.ToolResult{}, fmt.Errorf("downstream unavailable")
			}
			return core.ToolResult{Tool: "stepB", Output: json.RawMessage(`{"ok":true}`)}, nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-wf-retry", Prompt: "go"})
	if err == nil {
		t.Fatal("expected workflow failure")
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), "run-wf-retry")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusFailed {
		t.Fatalf("expected failed run, got %s", snapshot.Status)
	}
	mu.Lock()
	bShouldFail = false
	mu.Unlock()
	result, err := fw.RetryFailedRun(context.Background(), "run-wf-retry")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed run after retry, got %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if aCalls != 1 {
		t.Fatalf("node a already succeeded and must not re-execute, got %d calls", aCalls)
	}
	if bCalls != 2 {
		t.Fatalf("expected node b to run once per attempt, got %d calls", bCalls)
	}
}

func TestFrameworkWorkflowClassifiesExistingRunID(t *testing.T) {
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-wf-dup", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	_, err = fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-wf-dup", Prompt: "go"})
	if !errors.Is(err, agentflow.ErrRunAlreadyCompleted) {
		t.Fatalf("expected ErrRunAlreadyCompleted for duplicate run ID, got %v", err)
	}
}

func TestFrameworkRetryFailedHybridRunAutonomousPhase(t *testing.T) {
	scenario := core.Scenario{
		Name: "hybrid-retry",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Tools: map[string]core.Tool{
			"prep": {Name: "prep", Type: "builtin.echo"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationHybrid,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "prep", Kind: core.NodeTool, Ref: "prep", Input: json.RawMessage(`{"message":"data"}`)},
				},
			},
		},
	}
	prepCalls := 0
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(&flakyGateway{failures: 1, content: "recovered"}),
		agentflow.WithToolExecutor("prep", retryToolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
			prepCalls++
			return core.ToolResult{Tool: "prep", Output: json.RawMessage(`{"ok":true}`)}, nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-hybrid-retry", Agent: "assistant", Prompt: "go"})
	if err == nil {
		t.Fatal("expected autonomous phase failure")
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), "run-hybrid-retry")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusFailed {
		t.Fatalf("expected failed run, got %s", snapshot.Status)
	}
	result, err := fw.RetryFailedRun(context.Background(), "run-hybrid-retry")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "recovered" {
		t.Fatalf("expected recovered completion, got %+v", result)
	}
	if prepCalls != 1 {
		t.Fatalf("workflow phase already finished and must not re-execute, got %d prep calls", prepCalls)
	}
}
