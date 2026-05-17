package jwt

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

func TestAuthenticatorValidatesHS256BearerToken(t *testing.T) {
	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	authenticator, err := NewAuthenticator(Config{
		Issuer:   "https://issuer.example.test",
		Audience: "agentflow-api",
		Keys: []Key{{
			ID:         "key-1",
			Algorithm:  AlgorithmHS256,
			HMACSecret: []byte("secret-value"),
		}},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	token := signedHS256Token(t, "key-1", []byte("secret-value"), map[string]any{
		"iss":          "https://issuer.example.test",
		"aud":          []string{"agentflow-api", "other"},
		"sub":          "user-1",
		"exp":          now.Add(time.Hour).Unix(),
		"nbf":          now.Add(-time.Minute).Unix(),
		"tenant_id":    "tenant-a",
		"workspace_id": "workspace-a",
		"project_id":   "project-a",
		"roles":        []string{"operator", "viewer"},
	})

	principal, ok, err := authenticator.AuthenticateBearer(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected token to authenticate")
	}
	if principal.ID != "user-1" || principal.Type != identity.PrincipalUser {
		t.Fatalf("unexpected principal identity: %+v", principal)
	}
	if principal.Scope.TenantID != "tenant-a" || principal.Scope.WorkspaceID != "workspace-a" || principal.Scope.ProjectID != "project-a" {
		t.Fatalf("unexpected principal scope: %+v", principal.Scope)
	}
	if !principal.HasRole(identity.RoleOperator) || !principal.HasRole(identity.RoleViewer) {
		t.Fatalf("unexpected roles: %+v", principal.Roles)
	}
}

func TestAuthenticatorRejectsExpiredToken(t *testing.T) {
	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	authenticator, err := NewAuthenticator(Config{
		Issuer:   "https://issuer.example.test",
		Audience: "agentflow-api",
		Keys: []Key{{
			ID:         "key-1",
			Algorithm:  AlgorithmHS256,
			HMACSecret: []byte("secret-value"),
		}},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	token := signedHS256Token(t, "key-1", []byte("secret-value"), map[string]any{
		"iss":       "https://issuer.example.test",
		"aud":       "agentflow-api",
		"sub":       "user-1",
		"exp":       now.Add(-time.Minute).Unix(),
		"tenant_id": "tenant-a",
		"roles":     []string{"operator"},
	})

	_, ok, err := authenticator.AuthenticateBearer(context.Background(), token)
	if err == nil {
		t.Fatal("expected expired token error")
	}
	if ok {
		t.Fatal("expired token must not authenticate")
	}
}

func signedHS256Token(t *testing.T, kid string, secret []byte, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"typ": "JWT", "alg": "HS256", "kid": kid}
	segments := []string{encodeJWTPart(t, header), encodeJWTPart(t, claims)}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strings.Join(segments, ".")))
	segments = append(segments, base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
	return strings.Join(segments, ".")
}

func encodeJWTPart(t *testing.T, value any) string {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}
