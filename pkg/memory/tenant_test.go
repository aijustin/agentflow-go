package memory

import "testing"

func TestTenantScopedNamespace(t *testing.T) {
	ns := Namespace{
		Scope:     ScopeSession,
		SessionID: "demo:assistant",
		Agent:     "assistant",
	}
	scoped := TenantScopedNamespace(ns, "tenant-a")
	if scoped.TenantID != "tenant-a" || scoped.SessionID != ns.SessionID {
		t.Fatalf("unexpected scoped namespace: %+v", scoped)
	}
	if TenantScopedNamespace(scoped, "tenant-a") != scoped {
		t.Fatal("expected idempotent tenant prefix")
	}
}

func TestTenantNamespaceKeyCannotCollideWithConfiguredPrefix(t *testing.T) {
	scoped := TenantScopedNamespace(Namespace{
		Scope: ScopeSession, SessionID: "tenant-a/demo:assistant", Agent: "assistant",
	}, "tenant-a")
	unscoped := Namespace{
		Scope: ScopeSession, SessionID: "tenant-a/demo:assistant", Agent: "assistant",
	}
	if scoped.KeyPrefix() == unscoped.KeyPrefix() {
		t.Fatalf("tenant-scoped key collided with configured namespace: %q", scoped.KeyPrefix())
	}
	withSlash := TenantScopedNamespace(unscoped, "tenant/a")
	if withSlash.KeyPrefix() == scoped.KeyPrefix() {
		t.Fatalf("hierarchical tenant IDs collided: %q", withSlash.KeyPrefix())
	}
}
