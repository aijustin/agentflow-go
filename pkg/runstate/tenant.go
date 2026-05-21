package runstate

import (
	"context"
	"errors"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

var ErrTenantMismatch = errors.New("runstate: tenant mismatch")

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
func AuthorizeTenant(ctx context.Context, snapshot RunSnapshot) error {
	principal, ok := identity.PrincipalFromContext(ctx)
	if !ok || principal.Scope.TenantID == "" {
		return nil
	}
	if snapshot.TenantID == "" {
		return nil
	}
	if snapshot.TenantID != principal.Scope.TenantID {
		return ErrTenantMismatch
	}
	return nil
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
	return snapshot, nil
}
