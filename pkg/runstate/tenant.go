package runstate

import (
	"context"
	"errors"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

var (
	ErrTenantMismatch = errors.New("runstate: tenant mismatch")
	// ErrTenantRequired reports an access in tenant-strict mode where the
	// caller's context carries no tenant principal at all.
	ErrTenantRequired = errors.New("runstate: tenant identity required")
)

type tenantStrictKey struct{}

// ContextWithTenantStrictMode marks the context so AuthorizeTenant (and thus
// LoadAuthorized) rejects access when the caller has no tenant principal, or
// when the snapshot predates tenant stamping (empty TenantID). It is off by
// default for backward compatibility: without it, principal-less contexts
// and legacy unowned snapshots are accessible to anyone. Multi-tenant
// deployments should wrap every request context with this (e.g. in the auth
// middleware) so an unauthenticated or unstamped access path fails closed.
func ContextWithTenantStrictMode(ctx context.Context) context.Context {
	return context.WithValue(ctx, tenantStrictKey{}, true)
}

// TenantStrictModeFromContext reports whether ctx is tenant-strict.
func TenantStrictModeFromContext(ctx context.Context) bool {
	strict, _ := ctx.Value(tenantStrictKey{}).(bool)
	return strict
}

// StampTenant assigns the principal tenant to new snapshots when present in ctx.
func StampTenant(ctx context.Context, snapshot *RunSnapshot) {
	if snapshot == nil {
		return
	}
	principal, ok := identity.PrincipalFromContext(ctx)
	if !ok || principal.Scope.TenantID == "" {
		return
	}
	if snapshot.TenantID == "" {
		snapshot.TenantID = principal.Scope.TenantID
	}
}

// AuthorizeTenant ensures an authenticated principal can access the snapshot tenant.
// Legacy snapshots without tenant_id remain accessible when no principal is present.
// In tenant-strict mode (ContextWithTenantStrictMode) both cases fail closed:
// a missing principal yields ErrTenantRequired, an unstamped snapshot
// ErrTenantMismatch.
func AuthorizeTenant(ctx context.Context, snapshot RunSnapshot) error {
	principal, ok := identity.PrincipalFromContext(ctx)
	if !ok || principal.Scope.TenantID == "" {
		if TenantStrictModeFromContext(ctx) {
			return ErrTenantRequired
		}
		return nil
	}
	if snapshot.TenantID == "" {
		if TenantStrictModeFromContext(ctx) {
			return ErrTenantMismatch
		}
		return nil
	}
	if snapshot.TenantID != principal.Scope.TenantID {
		return ErrTenantMismatch
	}
	return nil
}

// ScopeListFilter binds a repository list operation to the authenticated
// tenant. Authenticated callers cannot select a different tenant; internal
// maintenance callers without a principal retain the explicitly requested
// scope unless tenant-strict mode is enabled.
func ScopeListFilter(ctx context.Context, filter ListFilter) (ListFilter, error) {
	principal, ok := identity.PrincipalFromContext(ctx)
	if !ok || principal.Scope.TenantID == "" {
		if TenantStrictModeFromContext(ctx) {
			return ListFilter{}, ErrTenantRequired
		}
		return filter, nil
	}
	if filter.TenantID != "" && filter.TenantID != principal.Scope.TenantID {
		return ListFilter{}, ErrTenantMismatch
	}
	filter.TenantID = principal.Scope.TenantID
	return filter, nil
}

// LoadAuthorized loads a snapshot and enforces tenant access when a principal is present.
func LoadAuthorized(ctx context.Context, repo Repository, runID string) (RunSnapshot, error) {
	snapshot, err := repo.Load(ctx, runID)
	if err != nil {
		return RunSnapshot{}, err
	}
	if err := AuthorizeTenant(ctx, snapshot); err != nil {
		return RunSnapshot{}, err
	}
	NormalizeSnapshot(&snapshot)
	return snapshot, nil
}
