package builder

import (
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// ToolBuilder configures a single tool declaration.
type ToolBuilder struct {
	parent *ScenarioBuilder
	name   string
	tool   core.Tool
}

func (tb *ToolBuilder) register() {
	if tb.parent == nil {
		return
	}
	if tb.parent.s.Tools == nil {
		tb.parent.s.Tools = make(map[string]core.Tool)
	}
	tb.parent.s.Tools[tb.name] = tb.tool
}

// Type sets the tool type (for example builtin.echo or http.client).
func (tb *ToolBuilder) Type(typ string) *ToolBuilder {
	tb.tool.Type = typ
	tb.register()
	return tb
}

// Description sets the tool description.
func (tb *ToolBuilder) Description(desc string) *ToolBuilder {
	tb.tool.Description = desc
	tb.register()
	return tb
}

// Approval sets the approval policy.
func (tb *ToolBuilder) Approval(policy core.ApprovalPolicy) *ToolBuilder {
	tb.tool.Approval = policy
	tb.register()
	return tb
}

// SideEffect sets the side effect level.
func (tb *ToolBuilder) SideEffect(level core.SideEffectLevel) *ToolBuilder {
	tb.tool.SideEffect = level
	tb.register()
	return tb
}

// LLM sets an optional LLM profile for tool-backed generation.
func (tb *ToolBuilder) LLM(ref string) *ToolBuilder {
	tb.tool.LLM = ref
	tb.register()
	return tb
}

// RateCap sets the per-run rate cap.
func (tb *ToolBuilder) RateCap(n int) *ToolBuilder {
	tb.tool.RateCap = n
	tb.register()
	return tb
}

// InputSchema sets the tool input JSON schema.
func (tb *ToolBuilder) InputSchema(schema json.RawMessage) *ToolBuilder {
	tb.tool.InputSchema = schema
	tb.register()
	return tb
}

// OutputSchema sets the tool output JSON schema.
func (tb *ToolBuilder) OutputSchema(schema json.RawMessage) *ToolBuilder {
	tb.tool.OutputSchema = schema
	tb.register()
	return tb
}

// Metadata sets tool metadata.
func (tb *ToolBuilder) Metadata(key, value string) *ToolBuilder {
	if tb.tool.Metadata == nil {
		tb.tool.Metadata = make(map[string]string)
	}
	tb.tool.Metadata[key] = value
	tb.register()
	return tb
}

// Done commits the tool and returns the parent scenario builder.
func (tb *ToolBuilder) Done() *ScenarioBuilder {
	tb.register()
	if tb.parent == nil {
		return nil
	}
	return tb.parent
}

// StatusTool commits the current tool and registers the status probe tool.
func (tb *ToolBuilder) StatusTool() *ToolBuilder {
	tb.register()
	return tb.parent.StatusTool()
}

// Agent commits the tool and starts configuring an agent.
func (tb *ToolBuilder) Agent(name string) *AgentBuilder {
	tb.register()
	return tb.parent.Agent(name)
}
