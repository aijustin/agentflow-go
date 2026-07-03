package core

import "fmt"

// CheckpointBeforeFinalAnswer pauses before the agent turn that produces the
// final answer (before any LLM call for that turn).
const CheckpointBeforeFinalAnswer = "before_final_answer"

// ToolApprovalDenialReason returns a non-empty reason when a tool call must be
// denied without pausing (for example approval=always with no human gate).
func ToolApprovalDenialReason(tool Tool) string {
	switch tool.Approval {
	case "", ApprovalNever, ApprovalPause:
		return ""
	case ApprovalAlways:
		return "tool requires approval"
	case ApprovalRisky:
		switch tool.SideEffect {
		case SideEffectWrite, SideEffectExternal, SideEffectDangerous:
			return "risky tool requires approval"
		default:
			return ""
		}
	default:
		return fmt.Sprintf("unsupported approval policy %q", tool.Approval)
	}
}

// ToolApprovalPauseRequired reports whether executing the tool should pause for
// human approval when a human gate is configured.
func ToolApprovalPauseRequired(tool Tool) bool {
	switch tool.Approval {
	case ApprovalPause:
		return true
	case ApprovalAlways:
		return true
	case ApprovalRisky:
		switch tool.SideEffect {
		case SideEffectWrite, SideEffectExternal, SideEffectDangerous:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

// HasHumanCheckpoint reports whether name appears in a checkpoint list.
func HasHumanCheckpoint(checkpoints []string, name string) bool {
	for _, checkpoint := range checkpoints {
		if checkpoint == name {
			return true
		}
	}
	return false
}
