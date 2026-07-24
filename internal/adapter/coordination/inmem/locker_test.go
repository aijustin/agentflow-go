package inmem

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/coordination"
)

func TestLockerAcquireRenewRelease(t *testing.T) {
	locker := NewLocker()
	ctx := context.Background()
	ttl := 200 * time.Millisecond

	lease, ok, err := locker.Acquire(ctx, "key-a", "owner-1", ttl)
	if err != nil || !ok {
		t.Fatalf("acquire failed: ok=%v err=%v", ok, err)
	}
	if lease.Key != "key-a" || lease.Owner != "owner-1" {
		t.Fatalf("unexpected lease: %+v", lease)
	}

	_, ok, err = locker.Acquire(ctx, "key-a", "owner-2", ttl)
	if err != nil || ok {
		t.Fatalf("second owner should not acquire held lease: ok=%v err=%v", ok, err)
	}

	// Wait out the wall-clock granularity (coarse on some platforms, e.g.
	// VMs) so the renew below computes a strictly later expiry.
	time.Sleep(time.Millisecond)
	renewed, ok, err := locker.Renew(ctx, lease, ttl)
	if err != nil || !ok {
		t.Fatalf("renew failed: ok=%v err=%v", ok, err)
	}
	if !renewed.ExpiresAt.After(lease.ExpiresAt) {
		t.Fatalf("renew should extend expiry: before=%v after=%v", lease.ExpiresAt, renewed.ExpiresAt)
	}
	if err := locker.Release(ctx, renewed); err != nil {
		t.Fatal(err)
	}
	_, ok, err = locker.Acquire(ctx, "key-a", "owner-2", ttl)
	if err != nil || !ok {
		t.Fatalf("lease should be free after release: ok=%v err=%v", ok, err)
	}
}

func TestLockerAcquireAfterExpiry(t *testing.T) {
	locker := NewLocker()
	ctx := context.Background()
	ttl := 50 * time.Millisecond

	lease, ok, err := locker.Acquire(ctx, "key-expire", "owner-1", ttl)
	if err != nil || !ok {
		t.Fatalf("acquire failed: ok=%v err=%v", ok, err)
	}
	time.Sleep(ttl + 20*time.Millisecond)

	_, ok, err = locker.Renew(ctx, lease, ttl)
	if err != nil || ok {
		t.Fatalf("renew on expired lease should fail softly: ok=%v err=%v", ok, err)
	}

	other, ok, err := locker.Acquire(ctx, "key-expire", "owner-2", ttl)
	if err != nil || !ok {
		t.Fatalf("new owner should acquire after expiry: ok=%v err=%v", ok, err)
	}
	if err := locker.Release(ctx, other); err != nil {
		t.Fatal(err)
	}
}

func TestLockerReentrantAcquireSameOwner(t *testing.T) {
	locker := NewLocker()
	ctx := context.Background()
	ttl := time.Minute

	first, ok, err := locker.Acquire(ctx, "key-re", "owner-1", ttl)
	if err != nil || !ok {
		t.Fatalf("first acquire failed: ok=%v err=%v", ok, err)
	}
	second, ok, err := locker.Acquire(ctx, "key-re", "owner-1", ttl)
	if err != nil || !ok {
		t.Fatalf("reentrant acquire failed: ok=%v err=%v", ok, err)
	}
	if second.Owner != "owner-1" {
		t.Fatalf("unexpected owner: %+v", second)
	}
	if err := locker.Release(ctx, first); err != nil {
		t.Fatal(err)
	}
}

func TestLockerValidationErrors(t *testing.T) {
	locker := NewLocker()
	ctx := context.Background()

	_, ok, err := locker.Acquire(ctx, "", "owner", time.Second)
	if err == nil || ok {
		t.Fatal("expected invalid lease for empty key")
	}
	if !errors.Is(err, coordination.ErrInvalidLease) {
		t.Fatalf("expected ErrInvalidLease, got %v", err)
	}

	lease := coordination.Lease{Key: "k", Owner: "wrong"}
	if err := locker.Release(ctx, lease); err == nil {
		t.Fatal("expected release error for unheld lease")
	}
}

func TestLockerTokensIncreaseMonotonically(t *testing.T) {
	locker := NewLocker()
	ctx := context.Background()
	ttl := time.Minute

	first, ok, err := locker.Acquire(ctx, "key-mono", "owner-1", ttl)
	if err != nil || !ok {
		t.Fatalf("first acquire failed: ok=%v err=%v", ok, err)
	}
	if first.Token == 0 {
		t.Fatal("acquire must mint a non-zero token")
	}
	if err := locker.Release(ctx, first); err != nil {
		t.Fatal(err)
	}
	second, ok, err := locker.Acquire(ctx, "key-mono", "owner-2", ttl)
	if err != nil || !ok {
		t.Fatalf("second acquire failed: ok=%v err=%v", ok, err)
	}
	if second.Token <= first.Token {
		t.Fatalf("token must increase across owners: first=%d second=%d", first.Token, second.Token)
	}
	// Renew keeps the token unchanged.
	renewed, ok, err := locker.Renew(ctx, second, ttl)
	if err != nil || !ok {
		t.Fatalf("renew failed: ok=%v err=%v", ok, err)
	}
	if renewed.Token != second.Token {
		t.Fatalf("renew must not change the token: before=%d after=%d", second.Token, renewed.Token)
	}
	if err := locker.Release(ctx, second); err != nil {
		t.Fatal(err)
	}
	third, ok, err := locker.Acquire(ctx, "key-mono", "owner-1", ttl)
	if err != nil || !ok {
		t.Fatalf("third acquire failed: ok=%v err=%v", ok, err)
	}
	if third.Token <= second.Token {
		t.Fatalf("token must keep increasing: second=%d third=%d", second.Token, third.Token)
	}
}

func TestLockerReentrantAcquireKeepsToken(t *testing.T) {
	locker := NewLocker()
	ctx := context.Background()
	ttl := time.Minute

	first, ok, err := locker.Acquire(ctx, "key-re-token", "owner-1", ttl)
	if err != nil || !ok {
		t.Fatalf("first acquire failed: ok=%v err=%v", ok, err)
	}
	second, ok, err := locker.Acquire(ctx, "key-re-token", "owner-1", ttl)
	if err != nil || !ok {
		t.Fatalf("reentrant acquire failed: ok=%v err=%v", ok, err)
	}
	if second.Token != first.Token {
		t.Fatalf("reentrant acquire must keep the token: first=%d second=%d", first.Token, second.Token)
	}
	// The original handle stays valid after the reentrant acquire.
	if err := locker.Release(ctx, first); err != nil {
		t.Fatal(err)
	}
}

func TestLockerRejectsStaleTokenAfterTakeover(t *testing.T) {
	locker := NewLocker()
	ctx := context.Background()
	ttl := 50 * time.Millisecond

	stale, ok, err := locker.Acquire(ctx, "key-takeover", "owner-1", ttl)
	if err != nil || !ok {
		t.Fatalf("acquire failed: ok=%v err=%v", ok, err)
	}
	time.Sleep(ttl + 20*time.Millisecond)
	current, ok, err := locker.Acquire(ctx, "key-takeover", "owner-2", time.Minute)
	if err != nil || !ok {
		t.Fatalf("takeover acquire failed: ok=%v err=%v", ok, err)
	}
	if _, ok, err := locker.Renew(ctx, stale, time.Minute); err != nil || ok {
		t.Fatalf("stale renew must fail softly: ok=%v err=%v", ok, err)
	}
	if err := locker.Release(ctx, stale); !errors.Is(err, coordination.ErrInvalidLease) {
		t.Fatalf("stale release must be rejected, got %v", err)
	}
	if _, ok, err := locker.Renew(ctx, current, time.Minute); err != nil || !ok {
		t.Fatalf("current holder renew must succeed: ok=%v err=%v", ok, err)
	}
}

func TestLockerRejectsDelayedRenewFromSameOwnerName(t *testing.T) {
	locker := NewLocker()
	ctx := context.Background()
	ttl := 50 * time.Millisecond

	// Same owner name on both sides, as in two processes sharing a config.
	first, ok, err := locker.Acquire(ctx, "key-dup-owner", "owner", ttl)
	if err != nil || !ok {
		t.Fatalf("first acquire failed: ok=%v err=%v", ok, err)
	}
	time.Sleep(ttl + 20*time.Millisecond)
	second, ok, err := locker.Acquire(ctx, "key-dup-owner", "owner", time.Minute)
	if err != nil || !ok {
		t.Fatalf("second acquire failed: ok=%v err=%v", ok, err)
	}
	if second.Token <= first.Token {
		t.Fatalf("token must increase across takeover: first=%d second=%d", first.Token, second.Token)
	}
	if _, ok, err := locker.Renew(ctx, first, time.Minute); err != nil || ok {
		t.Fatalf("delayed renew from same owner name must fail: ok=%v err=%v", ok, err)
	}
	if err := locker.Release(ctx, first); !errors.Is(err, coordination.ErrInvalidLease) {
		t.Fatalf("delayed release from same owner name must be rejected, got %v", err)
	}
}
