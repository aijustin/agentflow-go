package runstate

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidToken = errors.New("runstate: invalid token")
	// ErrTokenExpired classifies a well-formed but expired token. It wraps
	// ErrInvalidToken so existing errors.Is(err, ErrInvalidToken) checks keep
	// matching, while callers that care about expiry (e.g. HTTP 410 mapping)
	// can branch on the more specific sentinel.
	ErrTokenExpired = fmt.Errorf("%w: token expired", ErrInvalidToken)
)

// MinTokenSecretLength is the minimum HMAC secret length NewTokenSigner and
// NewTokenSignerWithRotation accept. HITL resume tokens are bearer
// credentials, so short, guessable secrets are rejected at construction
// instead of weakening every run's approval gate.
const MinTokenSecretLength = 16

type TokenPayload struct {
	RunID     string    `json:"run_id"`
	Version   int64     `json:"version"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// TokenSigner signs and verifies HITL resume tokens. A single-key signer
// (NewTokenSigner) keeps the legacy two-segment token format; a rotation
// signer (NewTokenSignerWithRotation) embeds a key id so in-flight tokens
// survive a key swap: new tokens are signed with the primary key, and both
// primary and secondary keys verify.
type TokenSigner struct {
	primary   []byte
	secondary []byte
	now       func() time.Time
}

func NewTokenSigner(secret []byte) (*TokenSigner, error) {
	if err := checkSecretStrength(secret); err != nil {
		return nil, err
	}
	return &TokenSigner{primary: secret, now: time.Now}, nil
}

// NewTokenSignerWithRotation creates a signer that signs with primary and
// verifies with both primary and secondary. To rotate: deploy with
// (newSecret, oldSecret), wait for every in-flight token signed by oldSecret
// to expire or be consumed, then redeploy with (newSecret) only — no token
// is invalidated mid-flight. Both secrets must meet MinTokenSecretLength.
func NewTokenSignerWithRotation(primary, secondary []byte) (*TokenSigner, error) {
	if err := checkSecretStrength(primary); err != nil {
		return nil, fmt.Errorf("runstate: primary %w", err)
	}
	if err := checkSecretStrength(secondary); err != nil {
		return nil, fmt.Errorf("runstate: secondary %w", err)
	}
	return &TokenSigner{primary: primary, secondary: secondary, now: time.Now}, nil
}

func checkSecretStrength(secret []byte) error {
	if len(secret) == 0 {
		return errors.New("runstate: token secret is required")
	}
	if len(secret) < MinTokenSecretLength {
		return fmt.Errorf("runstate: token secret must be at least %d bytes, got %d", MinTokenSecretLength, len(secret))
	}
	return nil
}

// tokenKeyID derives the public key identifier embedded in rotation tokens.
// It is a short digest of the key itself, so verifiers pick the right key
// without extra configuration and nothing secret is exposed beyond a
// 4-byte SHA-256 prefix.
func tokenKeyID(secret []byte) string {
	sum := sha256.Sum256(secret)
	return hex.EncodeToString(sum[:4])
}

func (s *TokenSigner) Sign(payload TokenPayload) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sig := signBody(s.primary, body)
	encoded := base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(sig)
	if s.secondary == nil {
		// Legacy two-segment format, unchanged for single-key deployments.
		return encoded, nil
	}
	return tokenKeyID(s.primary) + "." + encoded, nil
}

func signBody(secret, body []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	return mac.Sum(nil)
}

func (s *TokenSigner) Verify(token string) (TokenPayload, error) {
	parts := strings.Split(token, ".")
	var body []byte
	var err error
	switch len(parts) {
	case 2:
		body, err = s.verifySignature(parts[0], parts[1], s.candidateKeys()...)
	case 3:
		// kid.body.sig: only the key matching the embedded id may verify.
		secret := s.keyForID(parts[0])
		if secret == nil {
			return TokenPayload{}, ErrInvalidToken
		}
		body, err = s.verifySignature(parts[1], parts[2], secret)
	default:
		return TokenPayload{}, ErrInvalidToken
	}
	if err != nil {
		return TokenPayload{}, err
	}
	var payload TokenPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return TokenPayload{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !payload.ExpiresAt.IsZero() && s.now().After(payload.ExpiresAt) {
		return TokenPayload{}, ErrTokenExpired
	}
	return payload, nil
}

func (s *TokenSigner) candidateKeys() [][]byte {
	if s.secondary == nil {
		return [][]byte{s.primary}
	}
	return [][]byte{s.primary, s.secondary}
}

func (s *TokenSigner) keyForID(kid string) []byte {
	if tokenKeyID(s.primary) == kid {
		return s.primary
	}
	if s.secondary != nil && tokenKeyID(s.secondary) == kid {
		return s.secondary
	}
	return nil
}

func (s *TokenSigner) verifySignature(bodyPart, sigPart string, keys ...[]byte) ([]byte, error) {
	body, err := base64.RawURLEncoding.DecodeString(bodyPart)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	got, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	for _, secret := range keys {
		if secret != nil && hmac.Equal(got, signBody(secret, body)) {
			return body, nil
		}
	}
	return nil, ErrInvalidToken
}
