package security

import (
	"context"
	"errors"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

var (
	ErrUnauthenticated = errors.New("security: unauthenticated")
	ErrUnauthorized    = errors.New("security: unauthorized")
)

type Action string

const (
	ActionRunSubmit   Action = "run.submit"
	ActionRunRead     Action = "run.read"
	ActionRunCancel   Action = "run.cancel"
	ActionHITLResume  Action = "hitl.resume"
	ActionToolInvoke  Action = "tool.invoke"
	ActionMemoryRead  Action = "memory.read"
	ActionMemoryWrite Action = "memory.write"
	ActionAdminConfig Action = "admin.configure"
)

type Resource struct {
	Type        string            `json:"type"`
	ID          string            `json:"id,omitempty"`
	TenantID    string            `json:"tenant_id,omitempty"`
	WorkspaceID string            `json:"workspace_id,omitempty"`
	ProjectID   string            `json:"project_id,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type Policy interface {
	Authorize(ctx context.Context, principal identity.Principal, action Action, resource Resource) error
}

type APIKeyAuthenticator interface {
	AuthenticateAPIKey(ctx context.Context, key string) (identity.Principal, bool, error)
}

type BearerAuthenticator interface {
	AuthenticateBearer(ctx context.Context, token string) (identity.Principal, bool, error)
}

type APIKeyAuthenticatorFunc func(ctx context.Context, key string) (identity.Principal, bool, error)

func (fn APIKeyAuthenticatorFunc) AuthenticateAPIKey(ctx context.Context, key string) (identity.Principal, bool, error) {
	return fn(ctx, key)
}

type BearerAuthenticatorFunc func(ctx context.Context, token string) (identity.Principal, bool, error)

func (fn BearerAuthenticatorFunc) AuthenticateBearer(ctx context.Context, token string) (identity.Principal, bool, error) {
	return fn(ctx, token)
}

type PolicyFunc func(ctx context.Context, principal identity.Principal, action Action, resource Resource) error

func (fn PolicyFunc) Authorize(ctx context.Context, principal identity.Principal, action Action, resource Resource) error {
	return fn(ctx, principal, action, resource)
}

type RolePolicy struct {
	Rules map[Action][]identity.Role
}

func NewDefaultRolePolicy() RolePolicy {
	return RolePolicy{Rules: map[Action][]identity.Role{
		ActionRunSubmit:   {identity.RoleAdmin, identity.RoleOperator, identity.RoleService},
		ActionRunRead:     {identity.RoleAdmin, identity.RoleOperator, identity.RoleViewer, identity.RoleApprover, identity.RoleService},
		ActionRunCancel:   {identity.RoleAdmin, identity.RoleOperator, identity.RoleService},
		ActionHITLResume:  {identity.RoleAdmin, identity.RoleApprover},
		ActionToolInvoke:  {identity.RoleAdmin, identity.RoleOperator, identity.RoleService},
		ActionMemoryRead:  {identity.RoleAdmin, identity.RoleOperator, identity.RoleViewer, identity.RoleService},
		ActionMemoryWrite: {identity.RoleAdmin, identity.RoleOperator, identity.RoleService},
		ActionAdminConfig: {identity.RoleAdmin},
	}}
}

func (policy RolePolicy) Authorize(ctx context.Context, principal identity.Principal, action Action, resource Resource) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := principal.Validate(); err != nil {
		return ErrUnauthenticated
	}
	if resource.TenantID != "" && resource.TenantID != principal.Scope.TenantID {
		return ErrUnauthorized
	}
	allowedRoles, ok := policy.Rules[action]
	if !ok {
		return ErrUnauthorized
	}
	if principal.HasRole(identity.RoleAdmin) {
		return nil
	}
	if principal.HasAnyRole(allowedRoles...) {
		return nil
	}
	return ErrUnauthorized
}
