package jwt

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A token with no exp never expires: it stays valid for the lifetime of the
// signing key, and revoking it means rotating that key for every principal.
func TestAuthenticatorRejectsTokenWithoutExpClaim(t *testing.T) {
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
		"tenant_id": "tenant-a",
	})

	_, ok, err := authenticator.AuthenticateBearer(context.Background(), token)
	if ok {
		t.Fatal("expected a token without exp to be rejected")
	}
	if err == nil || !strings.Contains(err.Error(), "exp") {
		t.Fatalf("expected a missing-exp error, got %v", err)
	}
}

// An exp of zero is not "no expiry"; it is the Unix epoch, long past.
func TestAuthenticatorRejectsZeroExpClaim(t *testing.T) {
	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	authenticator, err := NewAuthenticator(Config{
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
		"sub":       "user-1",
		"tenant_id": "tenant-a",
		"exp":       0,
	})

	if _, ok, _ := authenticator.AuthenticateBearer(context.Background(), token); ok {
		t.Fatal("expected exp=0 to be rejected")
	}
}

func TestAuthenticatorAcceptsTokenWithFutureExp(t *testing.T) {
	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	authenticator, err := NewAuthenticator(Config{
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
		"sub":       "user-1",
		"tenant_id": "tenant-a",
		"exp":       now.Add(time.Hour).Unix(),
	})

	principal, ok, err := authenticator.AuthenticateBearer(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || principal.ID != "user-1" {
		t.Fatalf("expected the token to authenticate, got ok=%v principal=%+v", ok, principal)
	}
}
