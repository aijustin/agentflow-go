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

type tenantPermissiveKey struct{}

// ContextWithTenantPermissive marks the context as tenant-permissive, opting
// out of the default tenant-strict behavior: a principal-less caller may then
// access tenant-stamped snapshots and request explicit tenant-scoped list
// filters. It exists for trusted internal maintenance paths (retention
// sweeps, migration tooling, local/CLI diagnostics) that legitimately run
// without an authenticated principal. It never weakens checks when a
// principal IS present — cross-tenant access is still rejected.
//
// Strict mode is the default: every context is tenant-strict unless wrapped
// with this function.
func ContextWithTenantPermissive(ctx context.Context) context.Context {
	return context.WithValue(ctx, tenantPermissiveKey{}, true)
}

// TenantStrictModeFromContext reports whether ctx is tenant-strict. Strict is
// the default; ContextWithTenantPermissive opts out.
func TenantStrictModeFromContext(ctx context.Context) bool {
	permissive, _ := ctx.Value(tenantPermissiveKey{}).(bool)
	return !permissive
}

// StampTenant assigns the principal tenant to new snapshots when present in ctx.
func StampTenant(ctx context.Context, snapshot *RunSnapshot) {
	if snapshot == nil {
		return
	}
	principal, ok := identity.PrincipalFromContext(ctx)
	if !ok || principal.Validate() != nil {
		return
	}
	if snapshot.TenantID == "" {
		snapshot.TenantID = principal.Scope.TenantID
	}
}

// AuthorizeTenant ensures an authenticated principal can access the snapshot tenant.
// Snapshots carry a tenant stamp only when created under an authenticated
// principal, so tenant-strict mode (the default) fails closed exactly where
// data is protected: a principal-less caller touching a tenant-stamped
// snapshot yields ErrTenantRequired. Legacy unstamped snapshots remain
// accessible to anyone (single-tenant and internal paths keep working);
// ContextWithTenantPermissive restores full fail-open access for trusted
// maintenance callers.
func AuthorizeTenant(ctx context.Context, snapshot RunSnapshot) error {
	principal, ok := identity.PrincipalFromContext(ctx)
	if !ok {
		if snapshot.TenantID != "" && TenantStrictModeFromContext(ctx) {
			return ErrTenantRequired
		}
		return nil
	}
	if err := principal.Validate(); err != nil {
		return ErrTenantRequired
	}
	if snapshot.TenantID == "" {
		return nil
	}
	if snapshot.TenantID != principal.Scope.TenantID {
		return ErrTenantMismatch
	}
	return nil
}

// ScopeListFilter binds a repository list operation to the authenticated
// tenant. Authenticated callers cannot select a different tenant. A
// principal-less caller requesting an explicit tenant scope (protected data)
// is rejected in tenant-strict mode; a principal-less caller with an empty
// scope keeps the global maintenance view, and ContextWithTenantPermissive
// restores the legacy fail-open behavior for explicit scopes.
func ScopeListFilter(ctx context.Context, filter ListFilter) (ListFilter, error) {
	principal, ok := identity.PrincipalFromContext(ctx)
	if !ok {
		if filter.TenantID != "" && TenantStrictModeFromContext(ctx) {
			return ListFilter{}, ErrTenantRequired
		}
		return filter, nil
	}
	if err := principal.Validate(); err != nil {
		return ListFilter{}, ErrTenantRequired
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
