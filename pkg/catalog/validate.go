package catalog

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
)

var versionPattern = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+){0,2}(-[a-zA-Z0-9.-]+)?$`)

var supportedApprovalPolicies = []string{
	string(core.ApprovalNever),
	string(core.ApprovalRisky),
	string(core.ApprovalAlways),
	string(core.ApprovalPause),
}

var supportedSideEffects = []string{
	string(core.SideEffectNone),
	string(core.SideEffectRead),
	string(core.SideEffectWrite),
	string(core.SideEffectExternal),
	string(core.SideEffectDangerous),
}

// ValidateToolManifest checks a standalone tool manifest for catalog registration.
func ValidateToolManifest(tool core.Tool) error {
	if strings.TrimSpace(tool.Name) == "" {
		return fmt.Errorf("catalog: tool metadata.name is required")
	}
	if strings.TrimSpace(tool.Type) == "" {
		return fmt.Errorf("catalog: tool %q type is required", tool.Name)
	}
	if tool.Approval != "" && !containsString(supportedApprovalPolicies, string(tool.Approval)) {
		return fmt.Errorf("catalog: tool %q approval %q is unsupported", tool.Name, tool.Approval)
	}
	if tool.SideEffect != "" && !containsString(supportedSideEffects, string(tool.SideEffect)) {
		return fmt.Errorf("catalog: tool %q side_effect %q is unsupported", tool.Name, tool.SideEffect)
	}
	if tool.RateCap < 0 {
		return fmt.Errorf("catalog: tool %q rate_cap must be >= 0", tool.Name)
	}
	if len(tool.InputSchema) > 0 && !json.Valid(tool.InputSchema) {
		return fmt.Errorf("catalog: tool %q input_schema must be valid JSON", tool.Name)
	}
	if len(tool.OutputSchema) > 0 && !json.Valid(tool.OutputSchema) {
		return fmt.Errorf("catalog: tool %q output_schema must be valid JSON", tool.Name)
	}
	if tool.SideEffect == core.SideEffectDangerous && tool.Approval == core.ApprovalNever {
		return fmt.Errorf("catalog: tool %q with side_effect=dangerous requires approval other than never", tool.Name)
	}
	return nil
}

// ValidateSkillManifest checks a standalone skill manifest for catalog registration.
func ValidateSkillManifest(skill core.Skill) error {
	if strings.TrimSpace(skill.Name) == "" {
		return fmt.Errorf("catalog: skill metadata.name is required")
	}
	if skill.Version != "" && !versionPattern.MatchString(skill.Version) {
		return fmt.Errorf("catalog: skill %q version %q is invalid", skill.Name, skill.Version)
	}
	for index, fragment := range skill.PromptFragments {
		if strings.TrimSpace(fragment.Content) == "" {
			return fmt.Errorf("catalog: skill %q prompt_fragments[%d].content is required", skill.Name, index)
		}
	}
	for index, policy := range skill.ToolPolicies {
		if strings.TrimSpace(policy.Tool) == "" {
			return fmt.Errorf("catalog: skill %q tool_policies[%d].tool is required", skill.Name, index)
		}
		if policy.Approval != "" && !containsString(supportedApprovalPolicies, string(policy.Approval)) {
			return fmt.Errorf("catalog: skill %q tool_policies[%d].approval %q is unsupported", skill.Name, index, policy.Approval)
		}
		if policy.SideEffect != "" && !containsString(supportedSideEffects, string(policy.SideEffect)) {
			return fmt.Errorf("catalog: skill %q tool_policies[%d].side_effect %q is unsupported", skill.Name, index, policy.SideEffect)
		}
		if policy.RateCap < 0 {
			return fmt.Errorf("catalog: skill %q tool_policies[%d].rate_cap must be >= 0", skill.Name, index)
		}
	}
	return nil
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
