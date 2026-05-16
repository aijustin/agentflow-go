package runstate

import (
	"errors"
	"testing"
	"time"
)

func TestTokenSignerRoundTrip(t *testing.T) {
	signer, err := NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Sign(TokenPayload{RunID: "run-1", Version: 3})
	if err != nil {
		t.Fatal(err)
	}
	got, err := signer.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	if got.RunID != "run-1" || got.Version != 3 {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestTokenSignerRejectsTampering(t *testing.T) {
	signer, err := NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Sign(TokenPayload{RunID: "run-1", Version: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.Verify(token + "x"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected invalid token, got %v", err)
	}
}

func TestTokenSignerRejectsExpired(t *testing.T) {
	signer, err := NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Sign(TokenPayload{RunID: "run-1", Version: 3, ExpiresAt: time.Now().Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.Verify(token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected expired token error, got %v", err)
	}
}
