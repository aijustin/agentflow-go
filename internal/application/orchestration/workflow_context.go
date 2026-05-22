package orchestration

import "context"

type workflowContextKey struct{}

type workflowExecScope struct {
	stepPrefix string
}

func withStepPrefix(ctx context.Context, prefix string) context.Context {
	if prefix == "" {
		return ctx
	}
	scope := workflowExecScope{stepPrefix: prefix}
	if existing, ok := ctx.Value(workflowContextKey{}).(workflowExecScope); ok && existing.stepPrefix != "" {
		scope.stepPrefix = existing.stepPrefix + prefix
	}
	return context.WithValue(ctx, workflowContextKey{}, scope)
}

func stepPrefixFrom(ctx context.Context) string {
	scope, ok := ctx.Value(workflowContextKey{}).(workflowExecScope)
	if !ok {
		return ""
	}
	return scope.stepPrefix
}

func storageNodeID(ctx context.Context, nodeID string) string {
	prefix := stepPrefixFrom(ctx)
	if prefix == "" {
		return nodeID
	}
	return prefix + nodeID
}

func bareNodeID(storageID, prefix string) string {
	if prefix == "" || len(storageID) < len(prefix) {
		return storageID
	}
	if storageID[:len(prefix)] != prefix {
		return storageID
	}
	return storageID[len(prefix):]
}

const subgraphStepDelimiter = "::"

func subgraphStepPrefix(parentNodeID string) string {
	return parentNodeID + subgraphStepDelimiter
}
