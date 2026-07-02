package builder_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestBuilderOptionHelpers(t *testing.T) {
	temp := float32(0.2)
	scenario := builder.New("options-smoke").
		LLM("chat", builder.Provider("openai", "gpt"), builder.LLMEndpoint("https://api"), builder.LLMAPIKeyEnv("KEY"), builder.LLMTemperature(temp), builder.ChatCapabilities()).
		LLM("embed", builder.EmbedCapabilities()).
		LLM("selfrag", builder.SelfRAGChatContext()).
		Memory("session", builder.InMemoryMemory(builder.MemoryScopeSession), builder.MemoryNamespace("ns")).
		Memory("tiered", builder.TierSessionMemory("tier-ns", builder.TierHotCapacity(10))).
		Tool("http", builder.HTTPTool(), builder.ToolApproval(core.ApprovalAlways), builder.ToolSideEffect(builder.SideEffectRead)).Done().
		Skill("review", builder.SkillDescription("review code"), builder.SkillCompatibleAgents("assistant"), builder.SkillWorkflow(core.Workflow{})).
		KnowledgeCollection(builder.CollectionName("docs"), builder.CollectionNamespace("ns"), builder.CollectionTool("search"), builder.CollectionEmbedProfile("embed"), builder.CollectionSearchMode("hybrid")).
		Trigger(builder.TriggerEvent("ticket.created"), builder.TriggerAgent("assistant"), builder.TriggerDefaultPrompt("hi"), builder.TriggerPromptPath("p"), builder.TriggerContextPath("c"), builder.TriggerRunIDPath("id")).
		Agent("assistant").Instructions("go").Done().
		Autonomous().
		Scenario()

	if scenario.LLMs["chat"].Endpoint == "" || scenario.LLMs["selfrag"].Context.Strategy != contextwindow.StrategySlidingWindowWithSummary {
		t.Fatalf("unexpected llm options: chat=%+v selfrag=%+v", scenario.LLMs["chat"], scenario.LLMs["selfrag"])
	}
	if scenario.Memories["tiered"].Tiers == nil || !scenario.Memories["tiered"].Tiers.Enabled {
		t.Fatalf("unexpected tier memory: %+v", scenario.Memories["tiered"])
	}
	if scenario.Tools["http"].Type != builder.ToolTypeHTTPClient {
		t.Fatalf("unexpected tool: %+v", scenario.Tools["http"])
	}
	if len(scenario.Triggers) != 1 || scenario.Triggers[0].Event != "ticket.created" {
		t.Fatalf("unexpected triggers: %+v", scenario.Triggers)
	}
	if len(scenario.Knowledge.Collections) != 1 || scenario.Knowledge.Collections[0].SearchMode != "hybrid" {
		t.Fatalf("unexpected collection: %+v", scenario.Knowledge.Collections)
	}
}

func TestRuntimeAndOrchestrationOptions(t *testing.T) {
	timeout := 5 * time.Minute
	scenario := builder.New("runtime-options").
		DefaultMockLLM().
		Agent("assistant").Done().
		Runtime(builder.RuntimeTimeout(timeout)).
		Orchestration(builder.HumanInLoop(true)).
		Hybrid(builder.NewWorkflow().NodeTransform("a", json.RawMessage(`{}`)).Build()).
		Scenario()
	if scenario.Runtime.Timeout != timeout {
		t.Fatalf("timeout=%v", scenario.Runtime.Timeout)
	}
	if !scenario.Orchestration.HumanInLoop.Enabled || scenario.Orchestration.Mode != builder.ModeHybrid {
		t.Fatalf("orchestration=%+v", scenario.Orchestration)
	}
}
