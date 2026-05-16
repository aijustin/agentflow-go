package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aijustin/agentflow-go/internal/adapter/auth/apikey"
	"github.com/aijustin/agentflow-go/internal/adapter/debugui"
)

func TestTokenSecretForAddrAllowsLoopbackDevelopmentDefault(t *testing.T) {
	secret, err := tokenSecretForAddr("127.0.0.1:18080", "")
	if err != nil {
		t.Fatal(err)
	}
	if string(secret) != "dev-secret" {
		t.Fatalf("unexpected secret %q", string(secret))
	}
}

func TestTokenSecretForAddrRejectsExposedDevelopmentDefault(t *testing.T) {
	if _, err := tokenSecretForAddr(":18080", ""); err == nil {
		t.Fatal("expected exposed listener without explicit secret to fail")
	}
}

func TestListenURLFormatsAddresses(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string
	}{
		{name: "host port", addr: "127.0.0.1:18080", want: "http://127.0.0.1:18080"},
		{name: "wildcard port", addr: ":18080", want: "http://localhost:18080"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listenURL(tt.addr); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDebugOptionsForEnvAllowsLoopbackWithoutAPIKey(t *testing.T) {
	options, err := debugOptionsForEnv("127.0.0.1:18080", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if len(options) != 0 {
		t.Fatalf("expected no debug options, got %d", len(options))
	}
}

func TestDebugOptionsForEnvRequiresAPIKeyForExposedListener(t *testing.T) {
	_, err := debugOptionsForEnv(":18080", func(string) string { return "" })
	if err == nil {
		t.Fatal("expected exposed listener without API key to fail")
	}
}

func TestDebugOptionsForEnvProtectsDebugAPIWhenAPIKeyConfigured(t *testing.T) {
	options, err := debugOptionsForEnv("127.0.0.1:18080", func(key string) string {
		switch key {
		case "AGENT_HTTP_API_KEY":
			return "secret-key"
		case "AGENT_HTTP_TENANT_ID":
			return "tenant-1"
		case "AGENT_HTTP_PRINCIPAL_ID":
			return "svc-debug"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := debugui.New([]byte("secret"), options...)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewBufferString(`{"scenario_id":"autonomous","prompt":"hello"}`)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected protected API to reject missing key, got %d", unauthorized.Code)
	}
	authorizedReq := httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewBufferString(`{"scenario_id":"autonomous","prompt":"hello"}`))
	authorizedReq.Header.Set(apikey.DefaultHeaderName, "secret-key")
	authorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(authorized, authorizedReq)
	if authorized.Code != http.StatusOK {
		t.Fatalf("expected protected API to accept key, got %d %s", authorized.Code, authorized.Body.String())
	}
}
