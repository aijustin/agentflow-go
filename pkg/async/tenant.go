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
func StampTenant(ctx context.Context, job *Job) error {
	if job == nil {
		return nil
	}
	tenantID := TenantIDFromContext(ctx)
	if tenantID == "" {
		if runstate.TenantStrictModeFromContext(ctx) {
			return ErrTenantRequired
		}
		return nil
	}
	if job.TenantID != "" && job.TenantID != tenantID {
		return ErrTenantMismatch
	}
	job.TenantID = tenantID
	return nil
}

// AuthorizeTenant enforces ownership for tenant-scoped queue access. Worker
// and maintenance contexts without a principal remain global unless strict
// mode is explicitly enabled.
func AuthorizeTenant(ctx context.Context, job Job) error {
	tenantID := TenantIDFromContext(ctx)
	if tenantID == "" {
		if runstate.TenantStrictModeFromContext(ctx) {
			return ErrTenantRequired
		}
		return nil
	}
	if job.TenantID == "" || job.TenantID != tenantID {
		return ErrTenantMismatch
	}
	return nil
}

// ScopeJobFilter binds an admin listing to the authenticated tenant. Internal
// maintenance callers without a principal may still request an explicit
// tenant or leave it empty for a global view.
func ScopeJobFilter(ctx context.Context, filter JobFilter) (JobFilter, error) {
	tenantID := TenantIDFromContext(ctx)
	if tenantID == "" {
		if runstate.TenantStrictModeFromContext(ctx) {
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
