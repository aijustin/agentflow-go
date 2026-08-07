package async

import (
	"context"
	"errors"

	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

var (
	ErrTenantMismatch = errors.New("async: tenant mismatch")
	ErrTenantRequired = errors.New("async: tenant identity required")
)

// TenantIDFromContext returns the authenticated principal's tenant, if any.
func TenantIDFromContext(ctx context.Context) string {
	principal, ok := identity.PrincipalFromContext(ctx)
	if !ok {
		return ""
	}
	return principal.Scope.TenantID
}

// StampTenant assigns the authenticated principal tenant to a newly enqueued
// job. A caller cannot overwrite an explicitly supplied, different tenant.
// Principal-less callers enqueue unscoped (unprotected) jobs — tenant-strict
// mode fails closed on access to stamped jobs, never on creating new unowned
// ones.
func StampTenant(ctx context.Context, job *Job) error {
	if job == nil {
		return nil
	}
	tenantID := TenantIDFromContext(ctx)
	if tenantID == "" {
		return nil
	}
	if job.TenantID != "" && job.TenantID != tenantID {
		return ErrTenantMismatch
	}
	job.TenantID = tenantID
	return nil
}

// AuthorizeTenant enforces ownership for tenant-scoped queue access.
// Tenant-strict mode is the default (see runstate.TenantStrictModeFromContext):
// a principal-less caller touching a tenant-stamped job fails closed with
// ErrTenantRequired, while unscoped jobs stay accessible to worker and
// maintenance contexts. runstate.ContextWithTenantPermissive restores
// fail-open access for trusted internal callers.
func AuthorizeTenant(ctx context.Context, job Job) error {
	tenantID := TenantIDFromContext(ctx)
	if tenantID == "" {
		if job.TenantID != "" && runstate.TenantStrictModeFromContext(ctx) {
			return ErrTenantRequired
		}
		return nil
	}
	if job.TenantID == "" || job.TenantID != tenantID {
		return ErrTenantMismatch
	}
	return nil
}

// ScopeJobFilter binds an admin listing to the authenticated tenant. A
// principal-less caller requesting an explicit tenant scope is rejected in
// tenant-strict mode; an empty scope keeps the global maintenance view.
func ScopeJobFilter(ctx context.Context, filter JobFilter) (JobFilter, error) {
	tenantID := TenantIDFromContext(ctx)
	if tenantID == "" {
		if filter.TenantID != "" && runstate.TenantStrictModeFromContext(ctx) {
			return JobFilter{}, ErrTenantRequired
		}
		return filter, nil
	}
	if filter.TenantID != "" && filter.TenantID != tenantID {
		return JobFilter{}, ErrTenantMismatch
	}
	filter.TenantID = tenantID
	return filter, nil
}

func LoadAuthorized(ctx context.Context, queue Queue, jobID string) (Job, error) {
	job, err := queue.Load(ctx, jobID)
	if err != nil {
		return Job{}, err
	}
	if err := AuthorizeTenant(ctx, job); err != nil {
		return Job{}, err
	}
	return job, nil
}

func CancelAuthorized(ctx context.Context, queue Queue, jobID string) error {
	if _, err := LoadAuthorized(ctx, queue, jobID); err != nil {
		return err
	}
	return queue.Cancel(ctx, jobID)
}

func RequeueAuthorized(ctx context.Context, queue Queue, admin JobAdmin, jobID string) error {
	if _, err := LoadAuthorized(ctx, queue, jobID); err != nil {
		return err
	}
	return admin.Requeue(ctx, jobID)
}
