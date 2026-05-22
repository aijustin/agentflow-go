package builder

import (
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// ScenarioBuilder constructs a core.Scenario with a fluent API.
// Call Scenario() to obtain the result, then validate with agentflow.ValidateScenario.
type ScenarioBuilder struct {
	s            core.Scenario
	pendingAgent *AgentBuilder
}

// New creates a scenario builder with the given name.
func New(name string) *ScenarioBuilder {
	return &ScenarioBuilder{
		s: core.Scenario{
			Name:     name,
			LLMs:     make(map[string]core.LLMProfileRef),
			Memories: make(map[string]core.MemoryRef),
			Tools:    make(map[string]core.Tool),
			Skills:   make(map[string]core.Skill),
			Agents:   make(map[string]core.Agent),
		},
	}
}

// Description sets the scenario description.
func (b *ScenarioBuilder) Description(desc string) *ScenarioBuilder {
	b.commitAgent()
	b.s.Description = desc
	return b
}

// LLM registers an LLM profile reference.
func (b *ScenarioBuilder) LLM(name string, opts ...LLMOption) *ScenarioBuilder {
	b.commitAgent()
	if b.s.LLMs == nil {
		b.s.LLMs = make(map[string]core.LLMProfileRef)
	}
	profile := core.LLMProfileRef{}
	for _, opt := range opts {
		opt(&profile)
	}
	b.s.LLMs[name] = profile
	return b
}

// Memory registers a memory reference.
func (b *ScenarioBuilder) Memory(name string, opts ...MemoryOption) *ScenarioBuilder {
	b.commitAgent()
	if b.s.Memories == nil {
		b.s.Memories = make(map[string]core.MemoryRef)
	}
	ref := core.MemoryRef{}
	for _, opt := range opts {
		opt(&ref)
	}
	b.s.Memories[name] = ref
	return b
}

// Tool registers a tool declaration and returns a builder for further configuration.
func (b *ScenarioBuilder) Tool(name string, opts ...ToolOption) *ToolBuilder {
	b.commitAgent()
	tb := &ToolBuilder{
		parent: b,
		name:   name,
		tool:   core.Tool{Name: name},
	}
	for _, opt := range opts {
		opt(&tb.tool)
	}
	tb.register()
	return tb
}

// Agent starts configuring an agent. Chain agent options, then call another
// ScenarioBuilder method or Scenario() to register it.
func (b *ScenarioBuilder) Agent(name string) *AgentBuilder {
	b.commitAgent()
	return &AgentBuilder{
		parent: b,
		name:   name,
		agent:  core.Agent{Name: name},
	}
}

// Skill registers a skill declaration.
func (b *ScenarioBuilder) Skill(name string, opts ...SkillOption) *ScenarioBuilder {
	b.commitAgent()
	if b.s.Skills == nil {
		b.s.Skills = make(map[string]core.Skill)
	}
	skill := core.Skill{Name: name}
	for _, opt := range opts {
		opt(&skill)
	}
	b.s.Skills[name] = skill
	return b
}

// Trigger appends an event trigger.
func (b *ScenarioBuilder) Trigger(opts ...TriggerOption) *ScenarioBuilder {
	b.commitAgent()
	trigger := core.Trigger{}
	for _, opt := range opts {
		opt(&trigger)
	}
	b.s.Triggers = append(b.s.Triggers, trigger)
	return b
}

// KnowledgeCollection appends a knowledge collection binding.
func (b *ScenarioBuilder) KnowledgeCollection(opts ...KnowledgeCollectionOption) *ScenarioBuilder {
	b.commitAgent()
	collection := core.KnowledgeCollection{}
	for _, opt := range opts {
		opt(&collection)
	}
	b.s.Knowledge.Collections = append(b.s.Knowledge.Collections, collection)
	return b
}

// Orchestration sets orchestration policy.
func (b *ScenarioBuilder) Orchestration(opts ...OrchestrationOption) *ScenarioBuilder {
	b.commitAgent()
	for _, opt := range opts {
		opt(&b.s.Orchestration)
	}
	return b
}

// Runtime sets runtime policy.
func (b *ScenarioBuilder) Runtime(opts ...RuntimeOption) *ScenarioBuilder {
	b.commitAgent()
	for _, opt := range opts {
		opt(&b.s.Runtime)
	}
	return b
}

// Autonomous sets orchestration mode to autonomous.
func (b *ScenarioBuilder) Autonomous() *ScenarioBuilder {
	b.commitAgent()
	b.s.Orchestration.Mode = core.OrchestrationAutonomous
	return b
}

// FixedWorkflow sets fixed_workflow mode with the given workflow.
func (b *ScenarioBuilder) FixedWorkflow(wf core.Workflow) *ScenarioBuilder {
	b.commitAgent()
	b.s.Orchestration.Mode = core.OrchestrationFixedWorkflow
	b.s.Orchestration.Workflow = &wf
	return b
}

// Hybrid sets hybrid mode with an optional workflow phase.
func (b *ScenarioBuilder) Hybrid(wf core.Workflow) *ScenarioBuilder {
	b.commitAgent()
	b.s.Orchestration.Mode = core.OrchestrationHybrid
	b.s.Orchestration.Workflow = &wf
	return b
}

// Scenario returns the constructed scenario after committing any pending agent.
func (b *ScenarioBuilder) Scenario() core.Scenario {
	b.commitAgent()
	return b.s
}

// MustScenario returns the scenario or panics if basic invariants fail.
func (b *ScenarioBuilder) MustScenario() core.Scenario {
	s := b.Scenario()
	if err := sanityCheck(s); err != nil {
		panic(err)
	}
	return s
}

func (b *ScenarioBuilder) commitAgent() {
	if b.pendingAgent == nil {
		return
	}
	b.pendingAgent.commit()
	b.pendingAgent = nil
}

func sanityCheck(s core.Scenario) error {
	if s.Name == "" {
		return fmt.Errorf("builder: scenario name is required")
	}
	if len(s.Agents) == 0 {
		return fmt.Errorf("builder: at least one agent is required")
	}
	return nil
}
