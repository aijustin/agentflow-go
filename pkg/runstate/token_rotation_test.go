package runstate

import (
	"strings"
	"testing"
	"time"
)

var (
	rotationPrimary   = []byte("primary-secret-0123")
	rotationSecondary = []byte("secondary-secret-012")
)

// TestWeakSecretRejected: short secrets fail fast at construction instead of
// silently weakening every HITL gate.
func TestWeakSecretRejected(t *testing.T) {
	if _, err := NewTokenSigner([]byte("short")); err == nil {
		t.Fatal("expected weak secret rejection")
	}
	if _, err := NewTokenSigner(nil); err == nil {
		t.Fatal("expected empty secret rejection")
	}
	if _, err := NewTokenSignerWithRotation([]byte("short"), rotationSecondary); err == nil {
		t.Fatal("expected weak primary rejection")
	}
	if _, err := NewTokenSignerWithRotation(rotationPrimary, []byte("short")); err == nil {
		t.Fatal("expected weak secondary rejection")
	}
}

// TestRotationSignerVerifiesBothKeys: during rotation, tokens signed by the
// old key (legacy no-kid format) still verify, and new tokens carry the
// primary key id.
func TestRotationSignerVerifiesBothKeys(t *testing.T) {
	rotated, err := NewTokenSignerWithRotation(rotationPrimary, rotationSecondary)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := NewTokenSigner(rotationSecondary)
	if err != nil {
		t.Fatal(err)
	}

	payload := TokenPayload{RunID: "run-1", Version: 3, ExpiresAt: time.Now().Add(time.Hour)}

	// Token minted by the old single-key signer (no kid) must verify under
	// rotation via the secondary key.
	oldToken, err := legacy.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(oldToken, ".") != 1 {
		t.Fatalf("legacy signer must keep the two-segment format, got %q", oldToken)
	}
	if _, err := rotated.Verify(oldToken); err != nil {
		t.Fatalf("rotation signer must verify old-key tokens, got %v", err)
	}

	// Tokens minted under rotation carry the kid segment and verify.
	newToken, err := rotated.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(newToken, ".") != 2 {
		t.Fatalf("rotation signer must embed the key id, got %q", newToken)
	}
	got, err := rotated.Verify(newToken)
	if err != nil {
		t.Fatal(err)
	}
	if got.RunID != "run-1" || got.Version != 3 {
		t.Fatalf("payload mismatch: %+v", got)
	}

	// The old single-key signer must NOT accept kid-carrying tokens (clear
	// failure instead of silently accepting).
	if _, err := legacy.Verify(newToken); err == nil {
		t.Fatal("legacy signer must reject kid-carrying tokens")
	}
}

// TestRotationRejectsUnknownKeyID: a token whose kid matches neither key is
// invalid even with a well-formed signature shape.
func TestRotationRejectsUnknownKeyID(t *testing.T) {
	rotated, err := NewTokenSignerWithRotation(rotationPrimary, rotationSecondary)
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewTokenSignerWithRotation([]byte("other-secret-012345"), rotationSecondary)
	if err != nil {
		t.Fatal(err)
	}
	token, err := other.Sign(TokenPayload{RunID: "run-1", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.Verify(token); err == nil {
		t.Fatal("expected unknown-kid token rejection")
	}
}

// TestSingleKeySignerKeepsLegacyFormat: single-key deployments see no wire
// change, so a rolling upgrade never mixes formats.
func TestSingleKeySignerKeepsLegacyFormat(t *testing.T) {
	signer, err := NewTokenSigner(rotationPrimary)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Sign(TokenPayload{RunID: "run-1", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(token, ".") != 1 {
		t.Fatalf("single-key signer must emit two segments, got %q", token)
	}
	if _, err := signer.Verify(token); err != nil {
		t.Fatal(err)
	}
}
