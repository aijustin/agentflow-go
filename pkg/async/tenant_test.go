package async

import (
	"context"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestStampAndAuthorizeJobTenant(t *testing.T) {
	tenantA := jobTenantContext("tenant-a")
	tenantB := jobTenantContext("tenant-b")
	job := Job{ID: "job-1", Type: RunJobType}

	if err := StampTenant(tenantA, &job); err != nil {
		t.Fatal(err)
	}
	if job.TenantID != "tenant-a" {
		t.Fatalf("expected tenant-a, got %q", job.TenantID)
	}
	if err := AuthorizeTenant(tenantA, job); err != nil {
		t.Fatalf("same-tenant authorization failed: %v", err)
	}
	if err := AuthorizeTenant(tenantB, job); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected tenant mismatch, got %v", err)
	}
}

func TestJobTenantStrictModeRequiresStampedIdentity(t *testing.T) {
	strict := runstate.ContextWithTenantStrictMode(context.Background())
	if err := StampTenant(strict, &Job{}); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("expected tenant required while stamping, got %v", err)
	}
	if err := AuthorizeTenant(strict, Job{TenantID: "tenant-a"}); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("expected tenant required while authorizing, got %v", err)
	}
	if err := AuthorizeTenant(jobTenantContext("tenant-a"), Job{}); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected unstamped job mismatch, got %v", err)
	}
}

func TestStampJobTenantRejectsCallerOverride(t *testing.T) {
	job := Job{TenantID: "tenant-b"}
	if err := StampTenant(jobTenantContext("tenant-a"), &job); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected tenant mismatch, got %v", err)
	}
}

func TestScopeJobFilterBindsPrincipalTenant(t *testing.T) {
	filter, err := ScopeJobFilter(jobTenantContext("tenant-a"), JobFilter{State: JobQueued})
	if err != nil {
		t.Fatal(err)
	}
	if filter.TenantID != "tenant-a" {
		t.Fatalf("expected tenant-a filter, got %q", filter.TenantID)
	}
	if _, err := ScopeJobFilter(jobTenantContext("tenant-a"), JobFilter{TenantID: "tenant-b"}); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected tenant mismatch, got %v", err)
	}
}

func jobTenantContext(tenantID string) context.Context {
	return identity.WithPrincipal(context.Background(), identity.Principal{
		ID:   "principal-" + tenantID,
		Type: identity.PrincipalUser,
		Scope: identity.Scope{
			TenantID: tenantID,
		},
	})
}
