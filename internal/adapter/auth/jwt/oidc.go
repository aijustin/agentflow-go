package jwt

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

type OIDCConfig struct {
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

type OIDCAuthenticator struct {
	validator      *Authenticator
	issuer         string
	discoveryURL   string
	jwksURL        string
	client         *http.Client
	refreshEvery   time.Duration
	now            func() time.Time
	mu             sync.RWMutex
	keys           map[string]Key
	nextRefresh    time.Time
	discoveryReady bool
}

func NewOIDCAuthenticator(config OIDCConfig) (*OIDCAuthenticator, error) {
	if strings.TrimSpace(config.DiscoveryURL) == "" && strings.TrimSpace(config.JWKSURL) == "" {
		return nil, fmt.Errorf("oidc auth: discovery_url or jwks_url is required")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	refreshEvery := config.RefreshInterval
	if refreshEvery <= 0 {
		refreshEvery = 5 * time.Minute
	}
	principalType := config.PrincipalType
	if principalType == "" {
		principalType = identity.PrincipalUser
	}
	validator := &Authenticator{
		issuer:         config.Issuer,
		audience:       config.Audience,
		now:            now,
		leeway:         config.Leeway,
		principalType:  principalType,
		tenantClaim:    firstNonEmpty(config.TenantClaim, "tenant_id"),
		workspaceClaim: firstNonEmpty(config.WorkspaceClaim, "workspace_id"),
		projectClaim:   firstNonEmpty(config.ProjectClaim, "project_id"),
		rolesClaim:     firstNonEmpty(config.RolesClaim, "roles"),
	}
	return &OIDCAuthenticator{
		validator:    validator,
		issuer:       config.Issuer,
		discoveryURL: strings.TrimSpace(config.DiscoveryURL),
		jwksURL:      strings.TrimSpace(config.JWKSURL),
		client:       client,
		refreshEvery: refreshEvery,
		now:          now,
		keys:         make(map[string]Key),
	}, nil
}

func (auth *OIDCAuthenticator) AuthenticateBearer(ctx context.Context, token string) (identity.Principal, bool, error) {
	if err := ctx.Err(); err != nil {
		return identity.Principal{}, false, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return identity.Principal{}, false, nil
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return identity.Principal{}, false, fmt.Errorf("jwt auth: token must have three segments")
	}
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return identity.Principal{}, false, fmt.Errorf("jwt auth: decode header: %w", err)
	}
	var header tokenHeader
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return identity.Principal{}, false, fmt.Errorf("jwt auth: decode header json: %w", err)
	}
	key, ok, err := auth.keyFor(ctx, header, false)
	if err != nil {
		return identity.Principal{}, false, err
	}
	if !ok {
		key, ok, err = auth.keyFor(ctx, header, true)
		if err != nil {
			return identity.Principal{}, false, err
		}
		if !ok {
			return identity.Principal{}, false, fmt.Errorf("jwt auth: verification key not found")
		}
	}
	signed := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return identity.Principal{}, false, fmt.Errorf("jwt auth: decode signature: %w", err)
	}
	if err := verifySignature(key, []byte(signed), signature); err != nil {
		return identity.Principal{}, false, err
	}
	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return identity.Principal{}, false, fmt.Errorf("jwt auth: decode claims: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		return identity.Principal{}, false, fmt.Errorf("jwt auth: decode claims json: %w", err)
	}
	principal, err := auth.validator.principalFromClaims(claims)
	if err != nil {
		return identity.Principal{}, false, err
	}
	return principal, true, nil
}

func (auth *OIDCAuthenticator) keyFor(ctx context.Context, header tokenHeader, force bool) (Key, bool, error) {
	keys, err := auth.currentKeys(ctx, force)
	if err != nil {
		return Key{}, false, err
	}
	key, ok := keys[header.KeyID]
	if !ok && header.KeyID == "" && len(keys) == 1 {
		for _, candidate := range keys {
			key = candidate
			ok = true
		}
	}
	if !ok || key.Algorithm != header.Algorithm {
		return Key{}, false, nil
	}
	return key, true, nil
}

func (auth *OIDCAuthenticator) currentKeys(ctx context.Context, force bool) (map[string]Key, error) {
	now := auth.now().UTC()
	auth.mu.RLock()
	ready := len(auth.keys) > 0 && !now.After(auth.nextRefresh)
	if !force && ready {
		keys := cloneKeys(auth.keys)
		auth.mu.RUnlock()
		return keys, nil
	}
	auth.mu.RUnlock()

	auth.mu.Lock()
	defer auth.mu.Unlock()
	now = auth.now().UTC()
	if !force && len(auth.keys) > 0 && !now.After(auth.nextRefresh) {
		return cloneKeys(auth.keys), nil
	}
	if auth.jwksURL == "" || !auth.discoveryReady {
		if err := auth.discoverLocked(ctx); err != nil {
			return nil, err
		}
	}
	keys, err := auth.fetchJWKS(ctx)
	if err != nil {
		return nil, err
	}
	auth.keys = keys
	auth.nextRefresh = now.Add(auth.refreshEvery)
	return cloneKeys(keys), nil
}

func (auth *OIDCAuthenticator) discoverLocked(ctx context.Context) error {
	if auth.jwksURL != "" && auth.discoveryURL == "" {
		auth.discoveryReady = true
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, auth.discoveryURL, nil)
	if err != nil {
		return fmt.Errorf("oidc auth: discovery request: %w", err)
	}
	resp, err := auth.client.Do(req)
	if err != nil {
		return fmt.Errorf("oidc auth: discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("oidc auth: discovery status %d", resp.StatusCode)
	}
	var doc struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("oidc auth: decode discovery: %w", err)
	}
	if auth.issuer != "" && doc.Issuer != "" && doc.Issuer != auth.issuer {
		return fmt.Errorf("oidc auth: discovery issuer mismatch")
	}
	if strings.TrimSpace(doc.JWKSURI) == "" {
		return fmt.Errorf("oidc auth: discovery jwks_uri is required")
	}
	auth.jwksURL = strings.TrimSpace(doc.JWKSURI)
	auth.discoveryReady = true
	return nil
}

func (auth *OIDCAuthenticator) fetchJWKS(ctx context.Context) (map[string]Key, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, auth.jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("oidc auth: jwks request: %w", err)
	}
	resp, err := auth.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc auth: jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("oidc auth: jwks status %d", resp.StatusCode)
	}
	var set jwksDocument
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, fmt.Errorf("oidc auth: decode jwks: %w", err)
	}
	keys := make(map[string]Key, len(set.Keys))
	for _, jwk := range set.Keys {
		key, err := jwk.toKey()
		if err != nil {
			return nil, err
		}
		keys[key.ID] = key
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("oidc auth: jwks contains no usable keys")
	}
	return keys, nil
}

type jwksDocument struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	KeyType   string    `json:"kty"`
	KeyID     string    `json:"kid"`
	Algorithm Algorithm `json:"alg"`
	Use       string    `json:"use"`
	N         string    `json:"n"`
	E         string    `json:"e"`
}

func (j jwkKey) toKey() (Key, error) {
	if j.KeyType != "RSA" {
		return Key{}, fmt.Errorf("oidc auth: unsupported jwk kty %q", j.KeyType)
	}
	if j.KeyID == "" {
		return Key{}, fmt.Errorf("oidc auth: jwk kid is required")
	}
	algorithm := j.Algorithm
	if algorithm == "" {
		algorithm = AlgorithmRS256
	}
	if algorithm != AlgorithmRS256 {
		return Key{}, fmt.Errorf("oidc auth: unsupported jwk alg %q", algorithm)
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(j.N)
	if err != nil {
		return Key{}, fmt.Errorf("oidc auth: decode jwk n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(j.E)
	if err != nil {
		return Key{}, fmt.Errorf("oidc auth: decode jwk e: %w", err)
	}
	exponent := new(big.Int).SetBytes(eBytes).Int64()
	if exponent <= 0 {
		return Key{}, fmt.Errorf("oidc auth: invalid jwk exponent")
	}
	return Key{ID: j.KeyID, Algorithm: AlgorithmRS256, RSAPublicKey: &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(exponent)}}, nil
}

func cloneKeys(keys map[string]Key) map[string]Key {
	out := make(map[string]Key, len(keys))
	for id, key := range keys {
		out[id] = key
	}
	return out
}
