package scenario

import (
	"strings"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
)

func TestBuildMapsProfilesAndMemory(t *testing.T) {
	plan, err := Build(core.Scenario{
		Name: "scenario",
		LLMs: map[string]core.LLMProfileRef{
			"default": {Provider: "mock", Model: "test", Capabilities: []string{"chat", "embed", "unknown"}},
		},
		Memories: map[string]core.MemoryRef{
			"session": {Scope: string(memory.ScopeSession), Type: "in_memory"},
		},
		Agents: map[string]core.Agent{"assistant": {Name: "assistant"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.LLMs["default"].Model != "test" {
		t.Fatalf("unexpected llm plan: %+v", plan.LLMs)
	}
	if !llm.ProfileSupports(plan.LLMs["default"], llm.CapEmbed) || llm.ProfileSupports(plan.LLMs["default"], llm.CapStream) {
		t.Fatalf("unexpected capabilities: %+v", plan.LLMs["default"].Capabilities)
	}
	if plan.Memory["session"].Scope != memory.ScopeSession {
		t.Fatalf("unexpected memory plan: %+v", plan.Memory)
	}
}

func TestBuildRequiresAgent(t *testing.T) {
	_, err := Build(core.Scenario{Name: "scenario"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildExpandsSkillWorkflowAndPromptFragments(t *testing.T) {
	timeout := 2 * time.Minute
	plan, err := Build(core.Scenario{
		Name: "scenario",
		Skills: map[string]core.Skill{
			"review": {
				PromptFragments: []core.PromptFragment{{Name: "style", Content: "Be concise."}},
				AgentPolicy:     core.AgentPolicy{MaxSteps: 5, Timeout: timeout, RetryLimit: 2},
				ToolPolicies:    []core.SkillToolPolicy{{Tool: "echo", Approval: core.ApprovalRisky, RateCap: 2, SideEffect: core.SideEffectExternal}},
				Workflow: &core.Workflow{
					Nodes: []core.WorkflowNode{
						{ID: "inspect", Kind: core.NodeTool, Ref: "echo"},
						{ID: "summarize", Kind: core.NodeTransform, DependsOn: []string{"inspect"}},
					},
					Edges: []core.WorkflowEdge{{From: "inspect", To: "summarize"}},
				},
			},
		},
		Tools: map[string]core.Tool{"echo": {Name: "echo", Type: "builtin.echo", Approval: core.ApprovalNever}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", Instructions: "Base.", Skills: []string{"review"}},
		},
		Orchestration: core.Orchestration{Workflow: &core.Workflow{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := plan.Scenario.Agents["assistant"]
	if !strings.Contains(agent.Instructions, "Base.") || !strings.Contains(agent.Instructions, "Be concise.") {
		t.Fatalf("prompt fragments not merged: %q", agent.Instructions)
	}
	if agent.Policy.MaxSteps != 5 || agent.Policy.Timeout != timeout || agent.Policy.RetryLimit != 2 {
		t.Fatalf("agent policy not merged: %+v", agent.Policy)
	}
	nodes := plan.Scenario.Orchestration.Workflow.Nodes
	if len(nodes) != 2 {
		t.Fatalf("expected skill nodes expanded, got %+v", nodes)
	}
	if nodes[0].ID != "assistant.review.inspect" || nodes[1].DependsOn[0] != "assistant.review.inspect" {
		t.Fatalf("nodes not namespaced: %+v", nodes)
	}
	edges := plan.Scenario.Orchestration.Workflow.Edges
	if len(edges) != 1 || edges[0].From != "assistant.review.inspect" || edges[0].To != "assistant.review.summarize" {
		t.Fatalf("edges not namespaced: %+v", edges)
	}
	tool := plan.Scenario.Tools["echo"]
	if tool.Approval != core.ApprovalRisky || tool.RateCap != 2 || tool.SideEffect != core.SideEffectExternal {
		t.Fatalf("tool policy not applied: %+v", tool)
	}
}

func TestBuildRejectsIncompatibleSkill(t *testing.T) {
	_, err := Build(core.Scenario{
		Name:   "scenario",
		Skills: map[string]core.Skill{"review": {CompatibleAgents: []string{"assistant"}}},
		Agents: map[string]core.Agent{"worker": {Name: "worker", Skills: []string{"review"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "incompatible skill") {
		t.Fatalf("expected incompatible skill error, got %v", err)
	}
}
