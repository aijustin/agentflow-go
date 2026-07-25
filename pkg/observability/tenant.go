package observability

import (
	"context"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
)

// TenantIDFromContext returns the authenticated tenant carried by ctx.
func TenantIDFromContext(ctx context.Context) string {
	principal, ok := identity.PrincipalFromContext(ctx)
	if !ok {
		return ""
	}
	return strings.TrimSpace(principal.Scope.TenantID)
}

// StampEventTenant binds an unstamped event to the authenticated tenant.
// Principal-less local/CLI execution keeps the legacy empty tenant.
func StampEventTenant(ctx context.Context, event *core.Event) {
	if event == nil || strings.TrimSpace(event.TenantID) != "" {
		return
	}
	event.TenantID = TenantIDFromContext(ctx)
}
