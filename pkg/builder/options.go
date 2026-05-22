package builder

import (
	"time"

	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
)

// LLMOption configures an LLM profile reference.
type LLMOption func(*core.LLMProfileRef)

// Provider sets provider and model in one call.
func Provider(provider, model string) LLMOption {
	return func(p *core.LLMProfileRef) {
		p.Provider = provider
		p.Model = model
	}
}

// LLMProvider sets the LLM provider name.
func LLMProvider(name string) LLMOption {
	return func(p *core.LLMProfileRef) {
		p.Provider = name
	}
}

// LLMModel sets the model name.
func LLMModel(model string) LLMOption {
	return func(p *core.LLMProfileRef) {
		p.Model = model
	}
}

// LLMEndpoint sets a custom endpoint URL.
func LLMEndpoint(url string) LLMOption {
	return func(p *core.LLMProfileRef) {
		p.Endpoint = url
	}
}

// LLMAPIKeyEnv sets the environment variable name for the API key.
func LLMAPIKeyEnv(name string) LLMOption {
	return func(p *core.LLMProfileRef) {
		p.APIKeyEnv = name
	}
}

// LLMTemperature sets sampling temperature.
func LLMTemperature(v float32) LLMOption {
	return func(p *core.LLMProfileRef) {
		p.Temperature = &v
	}
}

// LLMCapabilities sets declared LLM capabilities.
func LLMCapabilities(caps ...string) LLMOption {
	return func(p *core.LLMProfileRef) {
		p.Capabilities = append([]string(nil), caps...)
	}
}

// ChatCapabilities sets chat and tool_call capabilities.
func ChatCapabilities() LLMOption {
	return LLMCapabilities(LLMCapChat, LLMCapToolCall)
}

// EmbedCapabilities sets embed capability.
func EmbedCapabilities() LLMOption {
	return LLMCapabilities(LLMCapEmbed)
}

// LLMContext sets context window governance on an LLM profile.
func LLMContext(policy contextwindow.Policy) LLMOption {
	return func(p *core.LLMProfileRef) {
		p.Context = policy
	}
}

// SelfRAGChatContext configures sliding_window_with_summary governance from self_rag.yaml.
func SelfRAGChatContext() LLMOption {
	return LLMContext(contextwindow.Policy{
		Strategy:          contextwindow.StrategySlidingWindowWithSummary,
		SummaryMode:       ContextSummaryModeLLM,
		StaleToolTurns:    2,
		ToolSchemaPruning: true,
	})
}

// MemoryOption configures a memory reference.
type MemoryOption func(*core.MemoryRef)

// InMemoryMemory configures an in_memory memory with the given scope.
func InMemoryMemory(scope string) MemoryOption {
	return func(m *core.MemoryRef) {
		m.Type = MemoryTypeInMemory
		m.Scope = scope
	}
}

// CustomMemory configures a custom memory backend reference.
func CustomMemory(scope string) MemoryOption {
	return func(m *core.MemoryRef) {
		m.Type = MemoryTypeCustom
		m.Scope = scope
	}
}

// MemoryNamespace sets the memory namespace.
func MemoryNamespace(ns string) MemoryOption {
	return func(m *core.MemoryRef) {
		m.Namespace = ns
	}
}

// TierSessionMemory configures custom tiered session memory.
func TierSessionMemory(namespace string, opts ...TierMemoryOption) MemoryOption {
	return func(m *core.MemoryRef) {
		m.Type = MemoryTypeCustom
		m.Scope = MemoryScopeSession
		m.Namespace = namespace
		settings := core.MemoryTierSettings{
			Enabled:       true,
			HotCapacity:   20,
			WarmCapacity:  100,
			ColdCapacity:  1000,
			PromoteAccess: 3,
			RecallBudget: core.MemoryTierRecallBudget{
				Total: 10,
				Hot:   8,
				Warm:  2,
			},
		}
		for _, opt := range opts {
			opt(&settings)
		}
		m.Tiers = &settings
	}
}

// ToolOption configures a tool declaration.
type ToolOption func(*core.Tool)

// BuiltinTool sets a builtin tool type.
func BuiltinTool(typ string) ToolOption {
	return func(t *core.Tool) {
		t.Type = typ
	}
}

// ToolApproval sets the tool approval policy.
func ToolApproval(policy core.ApprovalPolicy) ToolOption {
	return func(t *core.Tool) {
		t.Approval = policy
	}
}

// ToolSideEffect sets the tool side effect level.
func ToolSideEffect(level core.SideEffectLevel) ToolOption {
	return func(t *core.Tool) {
		t.SideEffect = level
	}
}

// HTTPTool marks a tool as http.client.
func HTTPTool() ToolOption {
	return BuiltinTool(ToolTypeHTTPClient)
}

// SkillOption configures a skill declaration.
type SkillOption func(*core.Skill)

// SkillDescription sets the skill description.
func SkillDescription(desc string) SkillOption {
	return func(s *core.Skill) {
		s.Description = desc
	}
}

// SkillWorkflow attaches a workflow to the skill.
func SkillWorkflow(wf core.Workflow) SkillOption {
	return func(s *core.Skill) {
		s.Workflow = &wf
	}
}

// SkillCompatibleAgents restricts compatible agents.
func SkillCompatibleAgents(agents ...string) SkillOption {
	return func(s *core.Skill) {
		s.CompatibleAgents = append([]string(nil), agents...)
	}
}

// TriggerOption configures an event trigger.
type TriggerOption func(*core.Trigger)

// TriggerEvent sets the trigger event type.
func TriggerEvent(event string) TriggerOption {
	return func(t *core.Trigger) {
		t.Event = event
	}
}

// TriggerAgent sets the target agent.
func TriggerAgent(agent string) TriggerOption {
	return func(t *core.Trigger) {
		t.Agent = agent
	}
}

// TriggerDefaultPrompt sets the default prompt when the event has no payload field.
func TriggerDefaultPrompt(prompt string) TriggerOption {
	return func(t *core.Trigger) {
		t.DefaultPrompt = prompt
	}
}

// TriggerPromptPath sets the JSONPath for the run prompt.
func TriggerPromptPath(path string) TriggerOption {
	return func(t *core.Trigger) {
		t.PromptPath = path
	}
}

// TriggerContextPath sets the JSONPath for run context.
func TriggerContextPath(path string) TriggerOption {
	return func(t *core.Trigger) {
		t.ContextPath = path
	}
}

// TriggerRunIDPath sets the JSONPath for the run id.
func TriggerRunIDPath(path string) TriggerOption {
	return func(t *core.Trigger) {
		t.RunIDPath = path
	}
}

// KnowledgeCollectionOption configures a knowledge collection.
type KnowledgeCollectionOption func(*core.KnowledgeCollection)

// CollectionName sets the collection name.
func CollectionName(name string) KnowledgeCollectionOption {
	return func(c *core.KnowledgeCollection) {
		c.Name = name
	}
}

// CollectionNamespace sets the vector namespace.
func CollectionNamespace(namespace string) KnowledgeCollectionOption {
	return func(c *core.KnowledgeCollection) {
		c.Namespace = namespace
	}
}

// CollectionTool sets the bound retriever tool name.
func CollectionTool(tool string) KnowledgeCollectionOption {
	return func(c *core.KnowledgeCollection) {
		c.Tool = tool
	}
}

// CollectionEmbedProfile sets the embed LLM profile reference.
func CollectionEmbedProfile(profile string) KnowledgeCollectionOption {
	return func(c *core.KnowledgeCollection) {
		c.EmbedProfile = profile
	}
}

// CollectionSearchMode sets vector or hybrid search.
func CollectionSearchMode(mode string) KnowledgeCollectionOption {
	return func(c *core.KnowledgeCollection) {
		c.SearchMode = mode
	}
}

// CollectionTenantScoped marks the collection tenant scoped.
func CollectionTenantScoped(enabled bool) KnowledgeCollectionOption {
	return func(c *core.KnowledgeCollection) {
		c.TenantScoped = enabled
	}
}

// CollectionAgents restricts bound agents.
func CollectionAgents(agents ...string) KnowledgeCollectionOption {
	return func(c *core.KnowledgeCollection) {
		c.Agents = append([]string(nil), agents...)
	}
}

// TierMemoryOption configures tier memory settings.
type TierMemoryOption func(*core.MemoryTierSettings)

// TierHotCapacity sets hot tier capacity.
func TierHotCapacity(n int) TierMemoryOption {
	return func(t *core.MemoryTierSettings) {
		t.HotCapacity = n
	}
}

// TierWarmCapacity sets warm tier capacity.
func TierWarmCapacity(n int) TierMemoryOption {
	return func(t *core.MemoryTierSettings) {
		t.WarmCapacity = n
	}
}

// TierColdCapacity sets cold tier capacity.
func TierColdCapacity(n int) TierMemoryOption {
	return func(t *core.MemoryTierSettings) {
		t.ColdCapacity = n
	}
}

// TierPromoteAccess sets promote_access threshold.
func TierPromoteAccess(n int) TierMemoryOption {
	return func(t *core.MemoryTierSettings) {
		t.PromoteAccess = n
	}
}

// TierRecallBudget sets recall budget totals.
func TierRecallBudget(total, hot, warm, cold int) TierMemoryOption {
	return func(t *core.MemoryTierSettings) {
		t.RecallBudget = core.MemoryTierRecallBudget{
			Total: total,
			Hot:   hot,
			Warm:  warm,
			Cold:  cold,
		}
	}
}

// OrchestrationOption configures orchestration policy.
type OrchestrationOption func(*core.Orchestration)

// Mode sets orchestration mode explicitly.
func Mode(mode core.OrchestrationMode) OrchestrationOption {
	return func(o *core.Orchestration) {
		o.Mode = mode
	}
}

// Workflow sets the orchestration workflow graph.
func Workflow(wf core.Workflow) OrchestrationOption {
	return func(o *core.Orchestration) {
		o.Workflow = &wf
	}
}

// MaxParallel sets orchestration max_parallel.
func MaxParallel(n int) OrchestrationOption {
	return func(o *core.Orchestration) {
		o.MaxParallel = n
	}
}

// HumanInLoop enables or disables human-in-the-loop orchestration.
func HumanInLoop(enabled bool, checkpoints ...string) OrchestrationOption {
	return func(o *core.Orchestration) {
		o.HumanInLoop = core.HumanInLoopPolicy{
			Enabled:     enabled,
			Checkpoints: append([]string(nil), checkpoints...),
		}
	}
}

// Planning enables planning with optional configuration.
func Planning(enabled bool, opts ...PlanningOption) OrchestrationOption {
	return func(o *core.Orchestration) {
		o.Planning.Enabled = enabled
		for _, opt := range opts {
			opt(&o.Planning)
		}
	}
}

// PlanningOption configures planning policy fields.
type PlanningOption func(*core.PlanningPolicy)

// PlanningAgent sets the dedicated planning agent.
func PlanningAgent(agent string) PlanningOption {
	return func(p *core.PlanningPolicy) {
		p.Agent = agent
	}
}

// PlanningExecute enables plan step tracking during the tool loop.
func PlanningExecute(enabled bool) PlanningOption {
	return func(p *core.PlanningPolicy) {
		p.Execute = enabled
	}
}

// PlanningMaxSteps sets planning max_steps.
func PlanningMaxSteps(n int) PlanningOption {
	return func(p *core.PlanningPolicy) {
		p.MaxSteps = n
	}
}

// RuntimeOption configures runtime policy.
type RuntimeOption func(*core.RuntimePolicy)

// RuntimeTimeout sets the global runtime timeout.
func RuntimeTimeout(d time.Duration) RuntimeOption {
	return func(r *core.RuntimePolicy) {
		r.Timeout = d
	}
}

// RuntimeMaxSteps sets the global max_steps.
func RuntimeMaxSteps(n int) RuntimeOption {
	return func(r *core.RuntimePolicy) {
		r.MaxSteps = n
	}
}

// RuntimeMaxRetries sets the global max_retries.
func RuntimeMaxRetries(n int) RuntimeOption {
	return func(r *core.RuntimePolicy) {
		r.MaxRetries = n
	}
}

// RuntimeSecret sets a runtime secret value.
func RuntimeSecret(key, value string) RuntimeOption {
	return func(r *core.RuntimePolicy) {
		if r.Secrets == nil {
			r.Secrets = make(map[string]string)
		}
		r.Secrets[key] = value
	}
}
