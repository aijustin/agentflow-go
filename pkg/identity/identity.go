package identity

import (
	"context"
	"errors"
)

var ErrPrincipalMissing = errors.New("identity: principal missing")

type PrincipalType string

const (
	PrincipalUser    PrincipalType = "user"
	PrincipalService PrincipalType = "service"
	PrincipalSystem  PrincipalType = "system"
)

type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
	RoleApprover Role = "approver"
	RoleService  Role = "service"
)

type Scope struct {
	TenantID    string `json:"tenant_id,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
}

type Principal struct {
	ID       string            `json:"id"`
	Type     PrincipalType     `json:"type"`
	Scope    Scope             `json:"scope"`
	Roles    []Role            `json:"roles,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func (p Principal) Validate() error {
	if p.ID == "" || p.Type == "" || p.Scope.TenantID == "" {
		return ErrPrincipalMissing
	}
	return nil
}

func (p Principal) HasRole(role Role) bool {
	for _, candidate := range p.Roles {
		if candidate == role {
			return true
		}
	}
	return false
}

func (p Principal) HasAnyRole(roles ...Role) bool {
	for _, role := range roles {
		if p.HasRole(role) {
			return true
		}
	}
	return false
}

type principalContextKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

func RequirePrincipal(ctx context.Context) (Principal, error) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		return Principal{}, ErrPrincipalMissing
	}
	if err := principal.Validate(); err != nil {
		return Principal{}, err
	}
	return principal, nil
}
