package jwt

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type MiddlewareConfig struct {
	Authenticator security.BearerAuthenticator
}

func NewMiddleware(config MiddlewareConfig) (func(http.Handler) http.Handler, error) {
	if config.Authenticator == nil {
		return nil, fmt.Errorf("jwt auth: authenticator is nil")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok, err := config.Authenticator.AuthenticateBearer(r.Context(), bearerTokenFromRequest(r))
			if err != nil || !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="agentflow"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(identity.WithPrincipal(r.Context(), principal)))
		})
	}, nil
}

func bearerTokenFromRequest(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if value == "" {
		return ""
	}
	prefix := "Bearer "
	if len(value) <= len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(value[len(prefix):])
}
