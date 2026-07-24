package core

import "context"

type idempotencyKeyContextKey struct{}

// WithIdempotencyKey attaches the idempotency key of the current tool
// execution to ctx. The runtime injects one key per logical tool execution
// before invoking the executor; side-effecting tools should deduplicate their
// effects by this key (upsert, dedupe table, or an idempotency-aware API).
//
// Stability contract: the key is unchanged when the same logical execution is
// replayed (recovery resume, node rerun via ResumeFromStep) and across the
// runtime's in-memory retries of one execution; a different logical execution
// (another node/iteration, or a new workflow node attempt) gets a different
// key. An empty key is never attached.
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, idempotencyKeyContextKey{}, key)
}

// IdempotencyKeyFromContext returns the idempotency key bound to ctx, if any.
func IdempotencyKeyFromContext(ctx context.Context) string {
	key, _ := ctx.Value(idempotencyKeyContextKey{}).(string)
	return key
}
