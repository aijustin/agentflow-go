package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	auditfile "github.com/aijustin/agentflow-go/internal/adapter/audit/file"
	"github.com/aijustin/agentflow-go/internal/adapter/auth/apikey"
	"github.com/aijustin/agentflow-go/internal/adapter/debugui"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

func main() {
	addr := os.Getenv("AGENT_HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:18080"
	}
	secret, err := tokenSecretForAddr(addr, os.Getenv("AGENT_TOKEN_SECRET"))
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	debugOptions, err := debugOptionsForEnv(addr, os.Getenv)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	debug, err := debugui.New(secret, debugOptions...)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           debug.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	_, _ = fmt.Fprintf(os.Stderr, "agentflow debug console listening on %s\n", listenURL(addr))
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func listenURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	if host == "" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func tokenSecretForAddr(addr, configured string) ([]byte, error) {
	if configured != "" {
		return []byte(configured), nil
	}
	if isLoopbackAddr(addr) {
		return []byte("dev-secret"), nil
	}
	return nil, fmt.Errorf("AGENT_TOKEN_SECRET is required when AGENT_HTTP_ADDR is not loopback")
}

func debugOptionsForEnv(addr string, getenv func(string) string) ([]debugui.Option, error) {
	apiKey := strings.TrimSpace(getenv("AGENT_HTTP_API_KEY"))
	if apiKey == "" {
		if isLoopbackAddr(addr) {
			return nil, nil
		}
		return nil, fmt.Errorf("AGENT_HTTP_API_KEY is required when AGENT_HTTP_ADDR is not loopback")
	}
	tenantID := strings.TrimSpace(getenv("AGENT_HTTP_TENANT_ID"))
	if tenantID == "" {
		tenantID = "default"
	}
	principalID := strings.TrimSpace(getenv("AGENT_HTTP_PRINCIPAL_ID"))
	if principalID == "" {
		principalID = "agent-http"
	}
	authenticator, err := apikey.NewStaticAuthenticator(map[string]identity.Principal{
		apiKey: {
			ID:    principalID,
			Type:  identity.PrincipalService,
			Scope: identity.Scope{TenantID: tenantID},
			Roles: []identity.Role{identity.RoleAdmin},
		},
	})
	if err != nil {
		return nil, err
	}
	authn, err := apikey.NewMiddleware(apikey.MiddlewareConfig{Authenticator: authenticator})
	if err != nil {
		return nil, err
	}
	options := []debugui.Option{
		debugui.WithHTTPMiddleware(authn),
		debugui.WithSecurityPolicy(security.NewDefaultRolePolicy()),
	}
	if auditPath := strings.TrimSpace(getenv("AGENT_HTTP_AUDIT_FILE")); auditPath != "" {
		sink, err := auditfile.NewSink(auditPath)
		if err != nil {
			return nil, err
		}
		options = append(options, debugui.WithAuditSink(sink))
	}
	return options, nil
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
