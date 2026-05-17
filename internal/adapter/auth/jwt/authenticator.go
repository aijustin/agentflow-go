package jwt

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

type Algorithm string

const (
	AlgorithmHS256 Algorithm = "HS256"
	AlgorithmRS256 Algorithm = "RS256"
)

type Key struct {
	ID              string
	Algorithm       Algorithm
	HMACSecret      []byte
	RSAPublicKey    *rsa.PublicKey
	RSAPublicKeyPEM []byte
}

type Config struct {
	Issuer         string
	Audience       string
	Keys           []Key
	Now            func() time.Time
	Leeway         time.Duration
	PrincipalType  identity.PrincipalType
	TenantClaim    string
	WorkspaceClaim string
	ProjectClaim   string
	RolesClaim     string
}

type Authenticator struct {
	issuer         string
	audience       string
	keys           map[string]Key
	now            func() time.Time
	leeway         time.Duration
	principalType  identity.PrincipalType
	tenantClaim    string
	workspaceClaim string
	projectClaim   string
	rolesClaim     string
}

type tokenHeader struct {
	Algorithm Algorithm `json:"alg"`
	KeyID     string    `json:"kid"`
}

func NewAuthenticator(config Config) (*Authenticator, error) {
	if len(config.Keys) == 0 {
		return nil, fmt.Errorf("jwt auth: at least one verification key is required")
	}
	keys := make(map[string]Key, len(config.Keys))
	for _, key := range config.Keys {
		if key.Algorithm == "" {
			return nil, fmt.Errorf("jwt auth: key algorithm is required")
		}
		if key.ID == "" && len(config.Keys) > 1 {
			return nil, fmt.Errorf("jwt auth: key id is required when multiple keys are configured")
		}
		prepared, err := prepareKey(key)
		if err != nil {
			return nil, err
		}
		keys[key.ID] = prepared
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	principalType := config.PrincipalType
	if principalType == "" {
		principalType = identity.PrincipalUser
	}
	auth := &Authenticator{
		issuer:         config.Issuer,
		audience:       config.Audience,
		keys:           keys,
		now:            now,
		leeway:         config.Leeway,
		principalType:  principalType,
		tenantClaim:    firstNonEmpty(config.TenantClaim, "tenant_id"),
		workspaceClaim: firstNonEmpty(config.WorkspaceClaim, "workspace_id"),
		projectClaim:   firstNonEmpty(config.ProjectClaim, "project_id"),
		rolesClaim:     firstNonEmpty(config.RolesClaim, "roles"),
	}
	return auth, nil
}

func (auth *Authenticator) AuthenticateBearer(ctx context.Context, token string) (identity.Principal, bool, error) {
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
	key, ok := auth.keyFor(header)
	if !ok {
		return identity.Principal{}, false, fmt.Errorf("jwt auth: verification key not found")
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
	principal, err := auth.principalFromClaims(claims)
	if err != nil {
		return identity.Principal{}, false, err
	}
	return principal, true, nil
}

func (auth *Authenticator) keyFor(header tokenHeader) (Key, bool) {
	key, ok := auth.keys[header.KeyID]
	if !ok && header.KeyID == "" && len(auth.keys) == 1 {
		for _, candidate := range auth.keys {
			key = candidate
			ok = true
		}
	}
	if !ok || key.Algorithm != header.Algorithm {
		return Key{}, false
	}
	return key, true
}

func (auth *Authenticator) principalFromClaims(claims map[string]any) (identity.Principal, error) {
	now := auth.now().UTC()
	if auth.issuer != "" && stringClaim(claims, "iss") != auth.issuer {
		return identity.Principal{}, fmt.Errorf("jwt auth: issuer mismatch")
	}
	if auth.audience != "" && !audienceContains(claims["aud"], auth.audience) {
		return identity.Principal{}, fmt.Errorf("jwt auth: audience mismatch")
	}
	if exp, ok := numericDate(claims["exp"]); ok && !now.Before(exp.Add(auth.leeway)) {
		return identity.Principal{}, fmt.Errorf("jwt auth: token expired")
	}
	if nbf, ok := numericDate(claims["nbf"]); ok && now.Add(auth.leeway).Before(nbf) {
		return identity.Principal{}, fmt.Errorf("jwt auth: token not yet valid")
	}
	principalType := auth.principalType
	if value := identity.PrincipalType(stringClaim(claims, "principal_type")); value != "" {
		principalType = value
	}
	principal := identity.Principal{
		ID:   stringClaim(claims, "sub"),
		Type: principalType,
		Scope: identity.Scope{
			TenantID:    stringClaim(claims, auth.tenantClaim),
			WorkspaceID: stringClaim(claims, auth.workspaceClaim),
			ProjectID:   stringClaim(claims, auth.projectClaim),
		},
		Roles: rolesClaim(claims[auth.rolesClaim]),
		Metadata: map[string]string{
			"issuer": auth.issuer,
		},
	}
	if err := principal.Validate(); err != nil {
		return identity.Principal{}, fmt.Errorf("jwt auth: invalid principal claims: %w", err)
	}
	return principal, nil
}

func prepareKey(key Key) (Key, error) {
	switch key.Algorithm {
	case AlgorithmHS256:
		if len(key.HMACSecret) == 0 {
			return Key{}, fmt.Errorf("jwt auth: hs256 key %q requires hmac secret", key.ID)
		}
		secret := make([]byte, len(key.HMACSecret))
		copy(secret, key.HMACSecret)
		key.HMACSecret = secret
	case AlgorithmRS256:
		if key.RSAPublicKey == nil {
			publicKey, err := parseRSAPublicKey(key.RSAPublicKeyPEM)
			if err != nil {
				return Key{}, fmt.Errorf("jwt auth: rsa key %q: %w", key.ID, err)
			}
			key.RSAPublicKey = publicKey
		}
	default:
		return Key{}, fmt.Errorf("jwt auth: unsupported algorithm %q", key.Algorithm)
	}
	return key, nil
}

func verifySignature(key Key, signed, signature []byte) error {
	sum := sha256.Sum256(signed)
	switch key.Algorithm {
	case AlgorithmHS256:
		mac := hmac.New(sha256.New, key.HMACSecret)
		mac.Write(signed)
		expected := mac.Sum(nil)
		if subtle.ConstantTimeCompare(expected, signature) != 1 {
			return fmt.Errorf("jwt auth: invalid signature")
		}
		return nil
	case AlgorithmRS256:
		if err := rsa.VerifyPKCS1v15(key.RSAPublicKey, crypto.SHA256, sum[:], signature); err != nil {
			return fmt.Errorf("jwt auth: invalid signature: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("jwt auth: unsupported algorithm %q", key.Algorithm)
	}
}

func parseRSAPublicKey(raw []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("missing PEM block")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		if pkcs1, pkcs1Err := x509.ParsePKCS1PublicKey(block.Bytes); pkcs1Err == nil {
			return pkcs1, nil
		}
		return nil, err
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is %T, not *rsa.PublicKey", parsed)
	}
	return publicKey, nil
}

func stringClaim(claims map[string]any, name string) string {
	value, _ := claims[name].(string)
	return strings.TrimSpace(value)
}

func rolesClaim(value any) []identity.Role {
	stringsValue := stringSliceClaim(value)
	roles := make([]identity.Role, 0, len(stringsValue))
	for _, value := range stringsValue {
		value = strings.TrimSpace(value)
		if value != "" {
			roles = append(roles, identity.Role(value))
		}
	}
	return roles
}

func stringSliceClaim(value any) []string {
	switch typed := value.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	case string:
		return strings.FieldsFunc(typed, func(r rune) bool { return r == ' ' || r == ',' })
	default:
		return nil
	}
}

func audienceContains(value any, expected string) bool {
	for _, audience := range stringSliceClaim(value) {
		if audience == expected {
			return true
		}
	}
	if text, ok := value.(string); ok {
		return text == expected
	}
	return false
}

func numericDate(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case float64:
		return time.Unix(int64(typed), 0).UTC(), true
	case int64:
		return time.Unix(typed, 0).UTC(), true
	case json.Number:
		seconds, err := typed.Int64()
		return time.Unix(seconds, 0).UTC(), err == nil
	default:
		return time.Time{}, false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
