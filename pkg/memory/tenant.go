package memory

import "strings"

// TenantScopedNamespace prefixes namespace identifiers with tenantID when present.
func TenantScopedNamespace(ns Namespace, tenantID string) Namespace {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return ns
	}
	if ns.SessionID != "" && !strings.HasPrefix(ns.SessionID, tenantID+"/") {
		ns.SessionID = tenantID + "/" + ns.SessionID
	}
	if ns.RunID != "" && !strings.HasPrefix(ns.RunID, tenantID+"/") {
		ns.RunID = tenantID + "/" + ns.RunID
	}
	return ns
}
