package security

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

func TestBindTenant(t *testing.T) {
	principal := identity.Principal{Scope: identity.Scope{TenantID: "tenant-1"}}
	resource := BindTenant(principal, Resource{Type: "run", ID: "run-1"})
	if resource.TenantID != "tenant-1" {
		t.Fatalf("tenant_id = %q, want tenant-1", resource.TenantID)
	}
	existing := Resource{Type: "run", ID: "run-1", TenantID: "tenant-2"}
	resource = BindTenant(principal, existing)
	if resource.TenantID != "tenant-2" {
		t.Fatalf("existing tenant should be preserved, got %q", resource.TenantID)
	}
}
