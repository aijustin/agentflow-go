package runstate

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidToken = errors.New("runstate: invalid token")

type TokenPayload struct {
	RunID     string    `json:"run_id"`
	Version   int64     `json:"version"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

type TokenSigner struct {
	secret []byte
	now    func() time.Time
}

func NewTokenSigner(secret []byte) (*TokenSigner, error) {
	if len(secret) == 0 {
		return nil, fmt.Errorf("runstate: token secret is required")
	}
	return &TokenSigner{secret: secret, now: time.Now}, nil
}

func (s *TokenSigner) Sign(payload TokenPayload) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(body)
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func (s *TokenSigner) Verify(token string) (TokenPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return TokenPayload{}, ErrInvalidToken
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return TokenPayload{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return TokenPayload{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return TokenPayload{}, ErrInvalidToken
	}
	var payload TokenPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return TokenPayload{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !payload.ExpiresAt.IsZero() && s.now().After(payload.ExpiresAt) {
		return TokenPayload{}, fmt.Errorf("%w: expired", ErrInvalidToken)
	}
	return payload, nil
}
