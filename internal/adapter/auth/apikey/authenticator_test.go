package apikey

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

func TestStaticAuthenticatorAuthenticatesKnownKey(t *testing.T) {
	auth, err := NewStaticAuthenticator(map[string]identity.Principal{
		"secret": {ID: "svc-1", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleService}},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, ok, err := auth.AuthenticateAPIKey(context.Background(), "secret")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || principal.ID != "svc-1" || !principal.HasRole(identity.RoleService) {
		t.Fatalf("unexpected principal ok=%v principal=%+v", ok, principal)
	}
	principal.Roles[0] = identity.RoleAdmin
	again, ok, err := auth.AuthenticateAPIKey(context.Background(), "secret")
	if err != nil || !ok {
		t.Fatalf("expected second auth, ok=%v err=%v", ok, err)
	}
	if again.HasRole(identity.RoleAdmin) {
		t.Fatal("principal mutation leaked into authenticator")
	}
}

func TestStaticAuthenticatorRejectsUnknownKey(t *testing.T) {
	auth, err := NewStaticAuthenticator(map[string]identity.Principal{
		"secret": {ID: "svc-1", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err := auth.AuthenticateAPIKey(context.Background(), "wrong")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected unknown key to be rejected")
	}
}

func TestNewStaticAuthenticatorValidatesInputs(t *testing.T) {
	if _, err := NewStaticAuthenticator(nil); err == nil {
		t.Fatal("expected missing keys error")
	}
	if _, err := NewStaticAuthenticator(map[string]identity.Principal{"": {ID: "svc", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant"}}}); err == nil {
		t.Fatal("expected empty key error")
	}
	if _, err := NewStaticAuthenticator(map[string]identity.Principal{"secret": {ID: "svc", Type: identity.PrincipalService}}); err == nil {
		t.Fatal("expected invalid principal error")
	}
}
