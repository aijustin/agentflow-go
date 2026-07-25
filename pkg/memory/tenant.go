package memory

import "strings"

// TenantScopedNamespace assigns tenantID as a dedicated namespace dimension.
// Session and run identifiers remain unchanged, so tenant strings and
// user-configured namespace prefixes cannot collide.
func TenantScopedNamespace(ns Namespace, tenantID string) Namespace {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return ns
	}
	ns.TenantID = tenantID
	return ns
}
