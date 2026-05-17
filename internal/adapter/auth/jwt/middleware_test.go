package jwt

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

func TestMiddlewareAddsPrincipalToRequestContext(t *testing.T) {
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
	middleware, err := NewMiddleware(MiddlewareConfig{Authenticator: authenticator})
	if err != nil {
		t.Fatal(err)
	}
	token := signedHS256Token(t, "key-1", []byte("secret-value"), map[string]any{
		"iss":       "https://issuer.example.test",
		"aud":       "agentflow-api",
		"sub":       "service-1",
		"exp":       now.Add(time.Hour).Unix(),
		"tenant_id": "tenant-a",
		"roles":     []string{"service"},
	})

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := identity.PrincipalFromContext(r.Context())
		if !ok {
			t.Fatal("missing principal")
		}
		if principal.ID != "service-1" || !principal.HasRole(identity.RoleService) {
			t.Fatalf("unexpected principal: %+v", principal)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
}
