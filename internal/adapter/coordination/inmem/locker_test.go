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
