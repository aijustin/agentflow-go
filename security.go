package agentflow

import (
	"net/http"

	authapikey "github.com/aijustin/agentflow-go/internal/adapter/auth/apikey"
	authzhttp "github.com/aijustin/agentflow-go/internal/adapter/security/http"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type APIKeyMiddlewareConfig struct {
	Authenticator security.APIKeyAuthenticator
	HeaderName    string
}

type AuthorizationMiddlewareConfig struct {
	Policy       security.Policy
	Action       security.Action
	Resource     security.Resource
	ResourceFunc func(*http.Request) security.Resource
	Audit        audit.Sink
}

func NewStaticAPIKeyAuthenticator(keys map[string]identity.Principal) (security.APIKeyAuthenticator, error) {
	return authapikey.NewStaticAuthenticator(keys)
}

func NewAPIKeyMiddleware(config APIKeyMiddlewareConfig) (func(http.Handler) http.Handler, error) {
	return authapikey.NewMiddleware(authapikey.MiddlewareConfig{
		Authenticator: config.Authenticator,
		HeaderName:    config.HeaderName,
	})
}

func NewAuthorizationMiddleware(config AuthorizationMiddlewareConfig) (func(http.Handler) http.Handler, error) {
	return authzhttp.NewMiddleware(authzhttp.MiddlewareConfig{
		Policy:       config.Policy,
		Action:       config.Action,
		Resource:     config.Resource,
		ResourceFunc: config.ResourceFunc,
		Audit:        config.Audit,
	})
}
