package agentflow

import (
	"net/http"
	"time"

	authapikey "github.com/aijustin/agentflow-go/internal/adapter/auth/apikey"
	authjwt "github.com/aijustin/agentflow-go/internal/adapter/auth/jwt"
	authzhttp "github.com/aijustin/agentflow-go/internal/adapter/security/http"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type JWTAlgorithm string

const (
	JWTAlgorithmHS256 JWTAlgorithm = "HS256"
	JWTAlgorithmRS256 JWTAlgorithm = "RS256"
)

type APIKeyMiddlewareConfig struct {
	Authenticator security.APIKeyAuthenticator
	HeaderName    string
}

type JWTKey struct {
	ID              string
	Algorithm       JWTAlgorithm
	HMACSecret      []byte
	RSAPublicKeyPEM []byte
}

type JWTAuthenticatorConfig struct {
	Issuer         string
	Audience       string
	Keys           []JWTKey
	Now            func() time.Time
	Leeway         time.Duration
	PrincipalType  identity.PrincipalType
	TenantClaim    string
	WorkspaceClaim string
	ProjectClaim   string
	RolesClaim     string
}

type OIDCJWTAuthenticatorConfig struct {
	Issuer          string
	Audience        string
	DiscoveryURL    string
	JWKSURL         string
	HTTPClient      *http.Client
	RefreshInterval time.Duration
	Now             func() time.Time
	Leeway          time.Duration
	PrincipalType   identity.PrincipalType
	TenantClaim     string
	WorkspaceClaim  string
	ProjectClaim    string
	RolesClaim      string
}

type JWTMiddlewareConfig struct {
	Authenticator security.BearerAuthenticator
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

func NewJWTAuthenticator(config JWTAuthenticatorConfig) (security.BearerAuthenticator, error) {
	keys := make([]authjwt.Key, 0, len(config.Keys))
	for _, key := range config.Keys {
		secret := make([]byte, len(key.HMACSecret))
		copy(secret, key.HMACSecret)
		pem := make([]byte, len(key.RSAPublicKeyPEM))
		copy(pem, key.RSAPublicKeyPEM)
		keys = append(keys, authjwt.Key{ID: key.ID, Algorithm: authjwt.Algorithm(key.Algorithm), HMACSecret: secret, RSAPublicKeyPEM: pem})
	}
	return authjwt.NewAuthenticator(authjwt.Config{
		Issuer:         config.Issuer,
		Audience:       config.Audience,
		Keys:           keys,
		Now:            config.Now,
		Leeway:         config.Leeway,
		PrincipalType:  config.PrincipalType,
		TenantClaim:    config.TenantClaim,
		WorkspaceClaim: config.WorkspaceClaim,
		ProjectClaim:   config.ProjectClaim,
		RolesClaim:     config.RolesClaim,
	})
}

// NewOIDCJWTAuthenticator creates a bearer authenticator that discovers and
// refreshes RSA verification keys from OIDC Discovery/JWKS endpoints.
func NewOIDCJWTAuthenticator(config OIDCJWTAuthenticatorConfig) (security.BearerAuthenticator, error) {
	return authjwt.NewOIDCAuthenticator(authjwt.OIDCConfig{
		Issuer:          config.Issuer,
		Audience:        config.Audience,
		DiscoveryURL:    config.DiscoveryURL,
		JWKSURL:         config.JWKSURL,
		HTTPClient:      config.HTTPClient,
		RefreshInterval: config.RefreshInterval,
		Now:             config.Now,
		Leeway:          config.Leeway,
		PrincipalType:   config.PrincipalType,
		TenantClaim:     config.TenantClaim,
		WorkspaceClaim:  config.WorkspaceClaim,
		ProjectClaim:    config.ProjectClaim,
		RolesClaim:      config.RolesClaim,
	})
}

func NewAPIKeyMiddleware(config APIKeyMiddlewareConfig) (func(http.Handler) http.Handler, error) {
	return authapikey.NewMiddleware(authapikey.MiddlewareConfig{
		Authenticator: config.Authenticator,
		HeaderName:    config.HeaderName,
	})
}

func NewJWTMiddleware(config JWTMiddlewareConfig) (func(http.Handler) http.Handler, error) {
	return authjwt.NewMiddleware(authjwt.MiddlewareConfig{Authenticator: config.Authenticator})
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
