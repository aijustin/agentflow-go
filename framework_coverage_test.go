package agentflow_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/adapters"
	tierinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/inmem"
	memoryinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/inmem"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestFrameworkRunStructuredAutonomous(t *testing.T) {
	scenario := core.Scenario{
		Name: "structured-auto",
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
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(structuredFakeGateway{payload: json.RawMessage(`{"answer":"auto"}`)}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.RunStructured(context.Background(), agentflow.RunRequest{
		RunID: "run-structured-auto", Agent: "assistant", Prompt: "go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || string(result.StructuredOutput) != `{"answer":"auto"}` {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestFrameworkStreamHybridAfterWorkflowPhase(t *testing.T) {
	scenario := core.Scenario{
		Name: "hybrid-stream",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationHybrid,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "prep", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"ready":true}}`)},
				},
			},
		},
	}
	gateway := &streamGateway{chunks: []llm.ChatChunk{{Content: "streamed"}, {Done: true}}}
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(gateway))
	if err != nil {
		t.Fatal(err)
	}
	ch, err := fw.Stream(context.Background(), agentflow.RunRequest{RunID: "hybrid-stream", Agent: "assistant", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for chunk := range ch {
		got += chunk.Content
	}
	if got != "streamed" {
		t.Fatalf("unexpected stream output %q", got)
	}
}

// TestFrameworkStreamHybridWithTimeoutKeepsStreamAlive ensures the framework
// timeout wraps only the synchronous workflow prepare phase. Previously,
// defer cancel() on the hybrid Stream path cancelled the engine ctx as soon
// as Stream returned the channel, truncating chunks / marking the run Cancelled.
func TestFrameworkStreamHybridWithTimeoutKeepsStreamAlive(t *testing.T) {
	scenario := core.Scenario{
		Name: "hybrid-stream-timeout",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Runtime: core.RuntimePolicy{Timeout: 30 * time.Second},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationHybrid,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "prep", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"ready":true}}`)},
				},
			},
		},
	}
	gateway := &streamGateway{chunks: []llm.ChatChunk{
		{Content: "hello"},
		{Content: " world"},
		{Done: true},
	}}
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(gateway))
	if err != nil {
		t.Fatal(err)
	}
	ch, err := fw.Stream(context.Background(), agentflow.RunRequest{
		RunID: "hybrid-stream-timeout", Agent: "assistant", Prompt: "go",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	var cancelled bool
	for chunk := range ch {
		got += chunk.Content
		if chunk.Error != "" {
			cancelled = true
		}
	}
	if got != "hello world" {
		t.Fatalf("unexpected stream output %q", got)
	}
	if cancelled {
		t.Fatal("stream should not be cancelled by prepare-phase timeout context")
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), "hybrid-stream-timeout")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status == runstate.RunStatusCancelled {
		t.Fatalf("run marked cancelled: %s", snapshot.Status)
	}
}

func TestFrameworkWithTierMemoryOption(t *testing.T) {
	store := tierinmem.NewStore()
	manager := tier.NewManager(store, tier.Policy{HotCapacity: 5}, tier.NoopMigrationObserver{})
	scenario := builder.MinimalAutonomous("assistant")
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(fakeGateway{content: "ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithTierMemory("session", manager),
		agentflow.WithTierStore("session", store, tier.Policy{HotCapacity: 5}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "tier-run", Agent: "assistant", Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "session", Agent: "assistant"}
	if _, err := manager.Recall(context.Background(), ns, "", tier.RecallBudget{Total: 1}.Normalize()); err != nil {
		t.Fatal(err)
	}
}

func TestFrameworkResumeAndContinueInvalidToken(t *testing.T) {
	fw, err := agentflow.New(
		builder.MinimalHumanInLoop("assistant"),
		agentflow.WithHITLTokenSecret([]byte("test-secret-012345"), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fw.ResumeAndContinue(context.Background(), "not-a-valid-token", core.DecisionApprove, nil)
	if err == nil {
		t.Fatal("expected invalid token error")
	}
}

func TestFrameworkRunStructuredFixedWorkflow(t *testing.T) {
	scenario := core.Scenario{
		Name: "structured-wf",
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
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "prep", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"ready":true}}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(structuredFakeGateway{payload: json.RawMessage(`{"answer":"wf"}`)}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.RunStructured(context.Background(), agentflow.RunRequest{
		RunID: "structured-wf-run", Agent: "assistant", Prompt: "go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || string(result.StructuredOutput) != `{"answer":"wf"}` {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestFrameworkMemoryAndTierOptions(t *testing.T) {
	cog := memoryinmem.NewCognitiveRepository()
	scenario := builder.MinimalAutonomous("assistant")
	scenario.Memories = map[string]core.MemoryRef{
		"session": {
			Type:      "custom",
			Scope:     string(memory.ScopeSession),
			Namespace: "tier-opt-session",
			Tiers:     &core.MemoryTierSettings{Enabled: true, HotCapacity: 2},
		},
	}
	scenario.Agents["assistant"] = core.Agent{
		Name: "assistant", LLM: "default", Memory: "session",
	}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(fakeGateway{content: "ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithCognitiveMemory("session", cog),
		agentflow.WithTierColdSummarizer("session", adapters.NewLLMTierSummarizer(fakeGateway{content: "summary"}, "default")),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "tier-opt-run", Agent: "assistant", Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
}

func TestFrameworkRunStructuredHybrid(t *testing.T) {
	scenario := core.Scenario{
		Name: "structured-hybrid",
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
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationHybrid,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "prep", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"ready":true}}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(structuredFakeGateway{payload: json.RawMessage(`{"answer":"hybrid"}`)}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.RunStructured(context.Background(), agentflow.RunRequest{
		RunID: "structured-hybrid-run", Agent: "assistant", Prompt: "go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestFrameworkStreamFixedWorkflow(t *testing.T) {
	scenario := core.Scenario{
		Name: "stream-wf",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "prep", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"ready":true}}`)},
				},
			},
		},
	}
	gateway := &streamGateway{chunks: []llm.ChatChunk{{Content: "wf-stream"}, {Done: true}}}
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(gateway))
	if err != nil {
		t.Fatal(err)
	}
	ch, err := fw.Stream(context.Background(), agentflow.RunRequest{RunID: "stream-wf-run", Agent: "assistant", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for chunk := range ch {
		got += chunk.Content
	}
	if got != "wf-stream" {
		t.Fatalf("unexpected stream output %q", got)
	}
}
