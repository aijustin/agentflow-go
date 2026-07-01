package orchestration

import "context"

type workflowContextKey struct{}

type workflowExecScope struct {
	stepPrefix string
	// skipCurrentNode marks a context used by a node that is running
	// concurrently with siblings in the same batch (DAG batch, parallel
	// group, or map branch). CurrentNodeID exists solely to track which
	// human-gate/interrupt node a paused run is waiting on; letting an
	// unrelated sibling's saveStepOutput overwrite it after a pause would
	// corrupt that pending-node bookkeeping, so any concurrently scheduled
	// node must skip the update and let only the pause path itself set it.
	skipCurrentNode bool
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

func withSkipCurrentNode(ctx context.Context) context.Context {
	scope, ok := ctx.Value(workflowContextKey{}).(workflowExecScope)
	if !ok {
		scope = workflowExecScope{}
	}
	scope.skipCurrentNode = true
	return context.WithValue(ctx, workflowContextKey{}, scope)
}

func skipCurrentNodeUpdate(ctx context.Context) bool {
	scope, ok := ctx.Value(workflowContextKey{}).(workflowExecScope)
	return ok && scope.skipCurrentNode
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
