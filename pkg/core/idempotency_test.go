package core

import (
	"context"
	"testing"
)

func TestIdempotencyKeyRoundTrip(t *testing.T) {
	ctx := context.Background()
	if got := IdempotencyKeyFromContext(ctx); got != "" {
		t.Fatalf("expected empty key on bare context, got %q", got)
	}
	ctx = WithIdempotencyKey(ctx, "run-1:call-1")
	if got := IdempotencyKeyFromContext(ctx); got != "run-1:call-1" {
		t.Fatalf("expected round-trip key, got %q", got)
	}
}

func TestWithIdempotencyKeyEmptyIsNoOp(t *testing.T) {
	ctx := context.Background()
	if next := WithIdempotencyKey(ctx, ""); next != ctx {
		t.Fatal("empty key must not attach a new context value")
	}
}

func TestWithIdempotencyKeyOverrides(t *testing.T) {
	ctx := WithIdempotencyKey(context.Background(), "run-1:node-a:1")
	ctx = WithIdempotencyKey(ctx, "run-1:node-b:1")
	if got := IdempotencyKeyFromContext(ctx); got != "run-1:node-b:1" {
		t.Fatalf("expected inner scope to override the key, got %q", got)
	}
}
