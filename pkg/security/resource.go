package security

import "github.com/aijustin/agentflow-go/pkg/identity"

// BindTenant attaches the principal tenant to a resource when the resource has no tenant.
func BindTenant(principal identity.Principal, resource Resource) Resource {
	if resource.TenantID == "" && principal.Scope.TenantID != "" {
		resource.TenantID = principal.Scope.TenantID
	}
	return resource
}
