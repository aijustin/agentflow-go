package jwt

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
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

func TestOIDCAuthenticatorDiscoversJWKSAndRefreshesUnknownKid(t *testing.T) {
	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	key1 := mustRSAKey(t)
	key2 := mustRSAKey(t)
	currentKey := key1
	currentKid := "key-1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"jwks_uri": "http://" + r.Host + "/jwks"})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{jwkFromRSA(currentKid, &currentKey.PublicKey)}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	authenticator, err := NewOIDCAuthenticator(OIDCConfig{
		Issuer:          "https://issuer.example.test",
		Audience:        "agentflow-api",
		DiscoveryURL:    server.URL + "/.well-known/openid-configuration",
		RefreshInterval: time.Hour,
		HTTPClient:      server.Client(),
		Now:             func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	token1 := signedRS256Token(t, "key-1", key1, map[string]any{
		"iss":       "https://issuer.example.test",
		"aud":       "agentflow-api",
		"sub":       "user-1",
		"tenant_id": "tenant-a",
		"exp":       now.Add(time.Hour).Unix(),
	})
	principal, ok, err := authenticator.AuthenticateBearer(context.Background(), token1)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || principal.ID != "user-1" {
		t.Fatalf("unexpected principal ok=%v principal=%+v", ok, principal)
	}

	currentKey = key2
	currentKid = "key-2"
	token2 := signedRS256Token(t, "key-2", key2, map[string]any{
		"iss":       "https://issuer.example.test",
		"aud":       "agentflow-api",
		"sub":       "user-2",
		"tenant_id": "tenant-a",
		"exp":       now.Add(time.Hour).Unix(),
	})
	principal, ok, err = authenticator.AuthenticateBearer(context.Background(), token2)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || principal.ID != "user-2" {
		t.Fatalf("expected refresh to authenticate rotated key, ok=%v principal=%+v", ok, principal)
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

func mustRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func signedRS256Token(t *testing.T, kid string, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"typ": "JWT", "alg": "RS256", "kid": kid}
	segments := []string{encodeJWTPart(t, header), encodeJWTPart(t, claims)}
	digest := sha256.Sum256([]byte(strings.Join(segments, ".")))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	segments = append(segments, base64.RawURLEncoding.EncodeToString(sig))
	return strings.Join(segments, ".")
}

func jwkFromRSA(kid string, key *rsa.PublicKey) map[string]any {
	return map[string]any{
		"kty": "RSA",
		"kid": kid,
		"alg": "RS256",
		"use": "sig",
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
}
