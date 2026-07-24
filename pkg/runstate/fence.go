package runstate

import "context"

type fenceTokenKey struct{}

// ContextWithFenceToken stamps the fencing token of the currently held run
// lease (coordination.Lease.Token) onto the context, so every snapshot save
// made while the lease is held can present it to a FencedRepository. A zero
// token disables fencing, matching a context with no token at all.
func ContextWithFenceToken(ctx context.Context, token uint64) context.Context {
	if token == 0 {
		return ctx
	}
	return context.WithValue(ctx, fenceTokenKey{}, token)
}

// FenceTokenFromContext returns the fencing token attached by
// ContextWithFenceToken, or 0 when the caller holds no lease (or the lease
// predates fencing).
func FenceTokenFromContext(ctx context.Context) uint64 {
	token, _ := ctx.Value(fenceTokenKey{}).(uint64)
	return token
}

// SaveWithFence persists a run snapshot with lease fencing when the context
// carries a fence token and the repository implements FencedRepository; a
// stale token fails with ErrStaleFence, which callers must not retry. When
// the context carries no token it behaves exactly like Repository.Save.
//
// When the repository does not implement FencedRepository (file, redis
// runstate) it falls back to a plain Save and reports fellBack=true so the
// caller can warn once: without fencing a superseded lease holder can still
// write, so multi-node deployments are unsafe.
func SaveWithFence(ctx context.Context, repo Repository, snapshot *RunSnapshot, expectedVersion int64) (fellBack bool, err error) {
	token := FenceTokenFromContext(ctx)
	if token == 0 {
		return false, repo.Save(ctx, snapshot, expectedVersion)
	}
	fenced, ok := repo.(FencedRepository)
	if !ok {
		return true, repo.Save(ctx, snapshot, expectedVersion)
	}
	return false, fenced.SaveFenced(ctx, snapshot, expectedVersion, token)
}
