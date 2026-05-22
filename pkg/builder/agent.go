package builder

import (
	"encoding/json"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// AgentBuilder configures a single agent.
type AgentBuilder struct {
	parent *ScenarioBuilder
	name   string
	agent  core.Agent
}

func (ab *AgentBuilder) commit() {
	if ab.parent == nil {
		return
	}
	if ab.parent.s.Agents == nil {
		ab.parent.s.Agents = make(map[string]core.Agent)
	}
	ab.parent.s.Agents[ab.name] = ab.agent
}

func (ab *AgentBuilder) registerPending() {
	if ab.parent != nil {
		ab.parent.pendingAgent = ab
	}
}

// Description sets the agent description.
func (ab *AgentBuilder) Description(desc string) *AgentBuilder {
	ab.registerPending()
	ab.agent.Description = desc
	return ab
}

// Role sets the agent role.
func (ab *AgentBuilder) Role(role string) *AgentBuilder {
	ab.registerPending()
	ab.agent.Role = role
	return ab
}

// Instructions sets agent instructions.
func (ab *AgentBuilder) Instructions(text string) *AgentBuilder {
	ab.registerPending()
	ab.agent.Instructions = text
	return ab
}

// LLM sets the LLM profile reference.
func (ab *AgentBuilder) LLM(ref string) *AgentBuilder {
	ab.registerPending()
	ab.agent.LLM = ref
	return ab
}

// Memory sets the memory reference.
func (ab *AgentBuilder) Memory(ref string) *AgentBuilder {
	ab.registerPending()
	ab.agent.Memory = ref
	return ab
}

// Tool appends a tool reference.
func (ab *AgentBuilder) Tool(name string) *AgentBuilder {
	ab.registerPending()
	ab.agent.Tools = append(ab.agent.Tools, name)
	return ab
}

// Tools replaces the tool list.
func (ab *AgentBuilder) Tools(names ...string) *AgentBuilder {
	ab.registerPending()
	ab.agent.Tools = append([]string(nil), names...)
	return ab
}

// Skill appends a skill reference.
func (ab *AgentBuilder) Skill(name string) *AgentBuilder {
	ab.registerPending()
	ab.agent.Skills = append(ab.agent.Skills, name)
	return ab
}

// Skills replaces the skill list.
func (ab *AgentBuilder) Skills(names ...string) *AgentBuilder {
	ab.registerPending()
	ab.agent.Skills = append([]string(nil), names...)
	return ab
}

// SubAgent appends a sub-agent reference.
func (ab *AgentBuilder) SubAgent(name string) *AgentBuilder {
	ab.registerPending()
	ab.agent.SubAgents = append(ab.agent.SubAgents, name)
	return ab
}

// Metadata sets agent metadata.
func (ab *AgentBuilder) Metadata(key, value string) *AgentBuilder {
	ab.registerPending()
	if ab.agent.Metadata == nil {
		ab.agent.Metadata = make(map[string]string)
	}
	ab.agent.Metadata[key] = value
	return ab
}

// MaxSteps sets the agent max_steps policy.
func (ab *AgentBuilder) MaxSteps(n int) *AgentBuilder {
	ab.registerPending()
	ab.agent.Policy.MaxSteps = n
	return ab
}

// Timeout sets the agent timeout policy.
func (ab *AgentBuilder) Timeout(d time.Duration) *AgentBuilder {
	ab.registerPending()
	ab.agent.Policy.Timeout = d
	return ab
}

// RetryLimit sets the agent retry_limit policy.
func (ab *AgentBuilder) RetryLimit(n int) *AgentBuilder {
	ab.registerPending()
	ab.agent.Policy.RetryLimit = n
	return ab
}

// OutputSchema sets the agent output JSON schema.
func (ab *AgentBuilder) OutputSchema(schema json.RawMessage) *AgentBuilder {
	ab.registerPending()
	ab.agent.Policy.OutputSchema = schema
	return ab
}

// HumanCheckpoint appends a human checkpoint name.
func (ab *AgentBuilder) HumanCheckpoint(name string) *AgentBuilder {
	ab.registerPending()
	ab.agent.Policy.HumanCheckpoints = append(ab.agent.Policy.HumanCheckpoints, name)
	return ab
}

// Done commits the agent and returns the parent scenario builder.
func (ab *AgentBuilder) Done() *ScenarioBuilder {
	if ab.parent == nil {
		return nil
	}
	ab.commit()
	ab.parent.pendingAgent = nil
	return ab.parent
}

func (ab *AgentBuilder) end() *ScenarioBuilder {
	return ab.Done()
}

// Autonomous commits the agent and sets autonomous orchestration mode.
func (ab *AgentBuilder) Autonomous() *ScenarioBuilder {
	return ab.end().Autonomous()
}

// FixedWorkflow commits the agent and sets fixed_workflow mode.
func (ab *AgentBuilder) FixedWorkflow(wf core.Workflow) *ScenarioBuilder {
	return ab.end().FixedWorkflow(wf)
}

// Hybrid commits the agent and sets hybrid mode.
func (ab *AgentBuilder) Hybrid(wf core.Workflow) *ScenarioBuilder {
	return ab.end().Hybrid(wf)
}

// Orchestration commits the agent and applies orchestration options.
func (ab *AgentBuilder) Orchestration(opts ...OrchestrationOption) *ScenarioBuilder {
	return ab.end().Orchestration(opts...)
}

// Scenario commits the agent and returns the built scenario.
func (ab *AgentBuilder) Scenario() core.Scenario {
	return ab.end().Scenario()
}
