package yaml

import (
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/schemas"
)

var (
	supportedMemoryTypes  = []string{"in_memory", "file", "custom"}
	supportedMemoryScopes = []string{
		string(memory.ScopeConversation),
		string(memory.ScopeSession),
		string(memory.ScopeLongTerm),
		string(memory.ScopeAudit),
	}
	supportedApprovalPolicies = []string{
		string(core.ApprovalNever),
		string(core.ApprovalRisky),
		string(core.ApprovalAlways),
	}
	supportedSideEffects = []string{
		string(core.SideEffectNone),
		string(core.SideEffectRead),
		string(core.SideEffectWrite),
		string(core.SideEffectExternal),
		string(core.SideEffectDangerous),
	}
	supportedOrchestrationModes = []string{
		string(core.OrchestrationAutonomous),
		string(core.OrchestrationFixedWorkflow),
		string(core.OrchestrationHybrid),
	}
	supportedWorkflowNodeKinds = []string{
		string(core.NodeAgent),
		string(core.NodeTool),
		string(core.NodeSkill),
		string(core.NodeHumanGate),
		string(core.NodeTransform),
	}
	supportedLLMCapabilities = []string{
		llm.CapChat.String(),
		llm.CapToolCall.String(),
		llm.CapStructuredOutput.String(),
		llm.CapStream.String(),
		llm.CapEmbed.String(),
	}
	supportedContextStrategies = []string{
		string(contextwindow.StrategyNone),
		string(contextwindow.StrategySlidingWindow),
		string(contextwindow.StrategySlidingWindowWithSummary),
	}
)

// ScenarioJSONSchema returns the JSON Schema used by editors, CI, and agentctl.
func ScenarioJSONSchema() []byte {
	return schemas.ScenarioJSONSchema()
}

// SupportedMemoryTypes returns memory backend types accepted by scenario YAML.
func SupportedMemoryTypes() []string {
	return cloneStrings(supportedMemoryTypes)
}

// SupportedMemoryScopes returns memory scopes accepted by scenario YAML.
func SupportedMemoryScopes() []string {
	return cloneStrings(supportedMemoryScopes)
}

// SupportedApprovalPolicies returns tool approval policy values accepted by scenario YAML.
func SupportedApprovalPolicies() []string {
	return cloneStrings(supportedApprovalPolicies)
}

// SupportedSideEffects returns tool side-effect levels accepted by scenario YAML.
func SupportedSideEffects() []string {
	return cloneStrings(supportedSideEffects)
}

// SupportedOrchestrationModes returns orchestration mode values accepted by scenario YAML.
func SupportedOrchestrationModes() []string {
	return cloneStrings(supportedOrchestrationModes)
}

// SupportedWorkflowNodeKinds returns workflow node kinds accepted by scenario YAML.
func SupportedWorkflowNodeKinds() []string {
	return cloneStrings(supportedWorkflowNodeKinds)
}

// SupportedLLMCapabilities returns LLM capability values accepted by scenario YAML.
func SupportedLLMCapabilities() []string {
	return cloneStrings(supportedLLMCapabilities)
}

// SupportedContextStrategies returns context window strategies accepted by scenario YAML.
func SupportedContextStrategies() []string {
	return cloneStrings(supportedContextStrategies)
}

func cloneStrings(values []string) []string {
	return append([]string(nil), values...)
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
