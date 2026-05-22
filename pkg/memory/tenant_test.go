package memory

import "testing"

func TestTenantScopedNamespace(t *testing.T) {
	ns := Namespace{
		Scope:     ScopeSession,
		SessionID: "demo:assistant",
		Agent:     "assistant",
	}
	scoped := TenantScopedNamespace(ns, "tenant-a")
	if scoped.SessionID != "tenant-a/demo:assistant" {
		t.Fatalf("session id = %q", scoped.SessionID)
	}
	if TenantScopedNamespace(scoped, "tenant-a").SessionID != scoped.SessionID {
		t.Fatal("expected idempotent tenant prefix")
	}
}
