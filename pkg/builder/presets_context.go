package builder

import (
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

// ContextGovernanceLLM configures the openai-compatible profile from
// catalog ID context-governance.
func ContextGovernanceLLM() LLMOption {
	return func(p *core.LLMProfileRef) {
		p.Provider = LLMProviderOpenAICompat
		p.Model = LLMModelQwen35B
		p.Endpoint = LLMEndpointLocalOpenAICompat
		p.APIKeyEnv = LLMEnvRealModelAPIKey
		p.ContextWindowTokens = 1400
		p.MaxOutputTokens = 1024
		temp := float32(0)
		topP := float32(0.8)
		p.Temperature = &temp
		p.TopP = &topP
		p.Thinking = llm.ThinkingConfig{
			Enabled:      true,
			BudgetTokens: 768,
		}
		p.Context = contextwindow.Policy{
			Strategy:               contextwindow.StrategySlidingWindowWithSummary,
			MaxInputTokens:         220,
			ReservedOutputTokens:   1024,
			SummaryTokens:          80,
			ToolResultMaxTokens:    400,
			MemoryRecallLimit:      8,
			SystemPromptProtection: true,
			Compression: contextwindow.CompressionPolicy{
				Enabled:      true,
				TriggerRatio: 0.5,
			},
		}
	}
}

// ContextGovernanceLLM registers the context governance demo LLM profile.
func (b *ScenarioBuilder) ContextGovernanceLLM() *ScenarioBuilder {
	return b.LLM(NameDefaultLLM, ContextGovernanceLLM())
}

// PlannerMockLLM registers the workflow planner LLM profile.
func (b *ScenarioBuilder) PlannerMockLLM() *ScenarioBuilder {
	return b.LLM(NamePlannerLLM, MockLLM())
}

// StatusTool registers a second builtin.echo tool used as a status probe.
func (b *ScenarioBuilder) StatusTool() *ToolBuilder {
	return b.Tool(NameStatusTool, EchoToolPreset())
}

// PlannerLLM wires the agent to NamePlannerLLM.
func (ab *AgentBuilder) PlannerLLM() *AgentBuilder {
	return ab.LLM(NamePlannerLLM)
}
