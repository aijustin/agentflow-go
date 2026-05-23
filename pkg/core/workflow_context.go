package core

import "context"

type workflowNodeContextKey struct{}

// ContextWithWorkflowNode attaches the active workflow node ID to ctx for observability.
func ContextWithWorkflowNode(ctx context.Context, nodeID string) context.Context {
	if nodeID == "" {
		return ctx
	}
	return context.WithValue(ctx, workflowNodeContextKey{}, nodeID)
}

// WorkflowNodeFromContext returns the workflow node ID bound to ctx, if any.
func WorkflowNodeFromContext(ctx context.Context) string {
	nodeID, _ := ctx.Value(workflowNodeContextKey{}).(string)
	return nodeID
}
