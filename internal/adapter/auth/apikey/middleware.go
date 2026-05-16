package apikey

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

const DefaultHeaderName = "X-Agentflow-API-Key"

type MiddlewareConfig struct {
	Authenticator security.APIKeyAuthenticator
	HeaderName    string
}

func NewMiddleware(config MiddlewareConfig) (func(http.Handler) http.Handler, error) {
	if config.Authenticator == nil {
		return nil, fmt.Errorf("apikey auth: authenticator is nil")
	}
	headerName := config.HeaderName
	if headerName == "" {
		headerName = DefaultHeaderName
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := apiKeyFromRequest(r, headerName)
			principal, ok, err := config.Authenticator.AuthenticateAPIKey(r.Context(), key)
			if err != nil {
				http.Error(w, "authentication failed", http.StatusInternalServerError)
				return
			}
			if !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="agentflow"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(identity.WithPrincipal(r.Context(), principal)))
		})
	}, nil
}

func apiKeyFromRequest(r *http.Request, headerName string) string {
	if value := strings.TrimSpace(r.Header.Get("Authorization")); value != "" {
		prefix := "Bearer "
		if len(value) > len(prefix) && strings.EqualFold(value[:len(prefix)], prefix) {
			return strings.TrimSpace(value[len(prefix):])
		}
	}
	return strings.TrimSpace(r.Header.Get(headerName))
}
