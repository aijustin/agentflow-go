package core

import "context"

type trustModeKey struct{}

// ContextWithTrustMode attaches a run-scoped trust mode (for example "full_trust")
// so tool-approval paths in both autonomous and workflow runtimes can honor it.
func ContextWithTrustMode(ctx context.Context, mode string) context.Context {
	if mode == "" {
		return ctx
	}
	return context.WithValue(ctx, trustModeKey{}, mode)
}

// TrustModeFromContext returns the trust mode previously attached with
// ContextWithTrustMode, or "" when unset.
func TrustModeFromContext(ctx context.Context) string {
	mode, _ := ctx.Value(trustModeKey{}).(string)
	return mode
}

// TrustModeFullTrust is the run-scoped mode that skips static tool-approval
// pauses (ApprovalPause / ApprovalAlways). Dynamic ToolApprovalEvaluator
// decisions (for example MCP auth or mandatory user-input tools) still apply.
const TrustModeFullTrust = "full_trust"
