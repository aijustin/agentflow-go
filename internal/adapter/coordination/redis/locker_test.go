package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/aijustin/agentflow-go/pkg/coordination"
)

func newTestLocker(t *testing.T, keyPrefix string) (*Locker, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	locker, err := NewLocker(Config{Addr: server.Addr(), KeyPrefix: keyPrefix})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = locker.Close() })
	return locker, server
}

func TestLockerAcquireRenewRelease(t *testing.T) {
	ctx := context.Background()
	locker, _ := newTestLocker(t, "agentflow:")
	lease, acquired, err := locker.Acquire(ctx, "run:1", "worker:1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("expected lease to be acquired")
	}
	if lease.Key != "run:1" || lease.Owner != "worker:1" || lease.Token == 0 || lease.ExpiresAt.IsZero() {
		t.Fatalf("unexpected lease: %+v", lease)
	}
	if _, acquired, err = locker.Acquire(ctx, "run:1", "worker:2", time.Minute); err != nil {
		t.Fatal(err)
	} else if acquired {
		t.Fatal("expected second acquire to fail")
	}
	renewed, ok, err := locker.Renew(ctx, lease, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !renewed.ExpiresAt.After(lease.ExpiresAt) {
		t.Fatalf("expected renewed lease, got ok=%v lease=%+v", ok, renewed)
	}
	if renewed.Token != lease.Token {
		t.Fatalf("renew must not change the token: before=%d after=%d", lease.Token, renewed.Token)
	}
	if err := locker.Release(ctx, renewed); err != nil {
		t.Fatal(err)
	}
	if _, acquired, err = locker.Acquire(ctx, "run:1", "worker:2", time.Minute); err != nil {
		t.Fatal(err)
	} else if !acquired {
		t.Fatal("expected acquire after release")
	}
}

func TestLockerTokensIncreaseMonotonically(t *testing.T) {
	ctx := context.Background()
	locker, _ := newTestLocker(t, "")
	first, ok, err := locker.Acquire(ctx, "run:mono", "worker:1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("first acquire failed: ok=%v err=%v", ok, err)
	}
	if err := locker.Release(ctx, first); err != nil {
		t.Fatal(err)
	}
	second, ok, err := locker.Acquire(ctx, "run:mono", "worker:2", time.Minute)
	if err != nil || !ok {
		t.Fatalf("second acquire failed: ok=%v err=%v", ok, err)
	}
	if second.Token <= first.Token {
		t.Fatalf("token must increase: first=%d second=%d", first.Token, second.Token)
	}
	if err := locker.Release(ctx, second); err != nil {
		t.Fatal(err)
	}
	third, ok, err := locker.Acquire(ctx, "run:mono", "worker:1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("third acquire failed: ok=%v err=%v", ok, err)
	}
	if third.Token <= second.Token {
		t.Fatalf("token must keep increasing: second=%d third=%d", second.Token, third.Token)
	}
}

func TestLockerRejectsStaleTokenRenewAndRelease(t *testing.T) {
	ctx := context.Background()
	locker, server := newTestLocker(t, "")
	stale, ok, err := locker.Acquire(ctx, "run:stale", "worker:1", 50*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("acquire failed: ok=%v err=%v", ok, err)
	}
	// Let the lease expire, then a new owner takes over with a fresh token.
	server.FastForward(time.Second)
	current, ok, err := locker.Acquire(ctx, "run:stale", "worker:2", time.Minute)
	if err != nil || !ok {
		t.Fatalf("takeover acquire failed: ok=%v err=%v", ok, err)
	}
	if current.Token <= stale.Token {
		t.Fatalf("takeover token must be larger: stale=%d current=%d", stale.Token, current.Token)
	}
	// The zombie holder wakes up and tries to renew: must be rejected even
	// though the owner names differ only by identity, because its token is
	// superseded.
	if _, ok, err := locker.Renew(ctx, stale, time.Minute); err != nil || ok {
		t.Fatalf("stale renew must fail softly: ok=%v err=%v", ok, err)
	}
	if err := locker.Release(ctx, stale); !errors.Is(err, coordination.ErrInvalidLease) {
		t.Fatalf("stale release must be rejected, got %v", err)
	}
	// The current holder is unaffected by the stale attempts.
	if _, ok, err := locker.Renew(ctx, current, time.Minute); err != nil || !ok {
		t.Fatalf("current holder renew must succeed: ok=%v err=%v", ok, err)
	}
}

func TestLockerRejectsDelayedRenewFromSameOwnerName(t *testing.T) {
	ctx := context.Background()
	locker, server := newTestLocker(t, "")
	// Two processes configured with the SAME owner name. The first acquires,
	// stalls past the TTL, and the second takes over.
	first, ok, err := locker.Acquire(ctx, "run:dup", "worker", 50*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("first acquire failed: ok=%v err=%v", ok, err)
	}
	server.FastForward(time.Second)
	second, ok, err := locker.Acquire(ctx, "run:dup", "worker", time.Minute)
	if err != nil || !ok {
		t.Fatalf("second acquire failed: ok=%v err=%v", ok, err)
	}
	if second.Token <= first.Token {
		t.Fatalf("token must increase across takeover: first=%d second=%d", first.Token, second.Token)
	}
	// With the old locker (value = bare owner string) this delayed renew
	// would have succeeded and extended the second process's lock. The
	// full-value comparison must reject it.
	if _, ok, err := locker.Renew(ctx, first, time.Minute); err != nil || ok {
		t.Fatalf("delayed renew from same owner name must fail: ok=%v err=%v", ok, err)
	}
	if err := locker.Release(ctx, first); !errors.Is(err, coordination.ErrInvalidLease) {
		t.Fatalf("delayed release from same owner name must be rejected, got %v", err)
	}
	// The second holder's lock survived the stale traffic.
	if _, ok, err := locker.Renew(ctx, second, time.Minute); err != nil || !ok {
		t.Fatalf("second holder renew must succeed: ok=%v err=%v", ok, err)
	}
}

func TestLockerAcquireAfterTTLExpiryGetsLargerToken(t *testing.T) {
	ctx := context.Background()
	locker, server := newTestLocker(t, "")
	first, ok, err := locker.Acquire(ctx, "run:ttl", "worker:1", 50*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("acquire failed: ok=%v err=%v", ok, err)
	}
	server.FastForward(time.Second)
	second, ok, err := locker.Acquire(ctx, "run:ttl", "worker:2", time.Minute)
	if err != nil || !ok {
		t.Fatalf("acquire after expiry failed: ok=%v err=%v", ok, err)
	}
	if second.Token <= first.Token {
		t.Fatalf("token must increase after expiry: first=%d second=%d", first.Token, second.Token)
	}
}

func TestLockerDoesNotReleaseLeaseOwnedByAnotherWorker(t *testing.T) {
	ctx := context.Background()
	locker, _ := newTestLocker(t, "")
	lease, acquired, err := locker.Acquire(ctx, "run:1", "worker:1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("expected lease to be acquired")
	}
	lease.Owner = "worker:2"
	if err := locker.Release(ctx, lease); !errors.Is(err, coordination.ErrInvalidLease) {
		t.Fatalf("expected invalid lease, got %v", err)
	}
	if _, acquired, err = locker.Acquire(ctx, "run:1", "worker:2", time.Minute); err != nil {
		t.Fatal(err)
	} else if acquired {
		t.Fatal("wrong owner should not release the lease")
	}
}

func TestLockerValidatesInputs(t *testing.T) {
	if _, err := NewLocker(Config{}); err == nil {
		t.Fatal("expected missing address error")
	}
	if _, err := NewLocker(Config{Addr: "127.0.0.1:6379", DB: -1}); err == nil {
		t.Fatal("expected invalid db error")
	}
	if _, err := NewLockerFromClient(nil, ""); err == nil {
		t.Fatal("expected nil client error")
	}
	locker, _ := newTestLocker(t, "")
	if _, _, err := locker.Acquire(context.Background(), "", "owner", time.Minute); !errors.Is(err, coordination.ErrInvalidLease) {
		t.Fatalf("expected invalid key error, got %v", err)
	}
	if _, _, err := locker.Acquire(context.Background(), "key", "owner", 0); !errors.Is(err, coordination.ErrInvalidLease) {
		t.Fatalf("expected invalid ttl error, got %v", err)
	}
	if _, _, err := locker.Renew(context.Background(), coordination.Lease{Key: "key", Owner: "owner", Token: 1}, 0); !errors.Is(err, coordination.ErrInvalidLease) {
		t.Fatalf("expected invalid renew ttl error, got %v", err)
	}
	// A hand-built lease without a fencing token is not a valid lease.
	if _, _, err := locker.Renew(context.Background(), coordination.Lease{Key: "key", Owner: "owner"}, time.Minute); !errors.Is(err, coordination.ErrInvalidLease) {
		t.Fatalf("expected zero-token renew error, got %v", err)
	}
	if err := locker.Release(context.Background(), coordination.Lease{Key: "key", Owner: "owner"}); !errors.Is(err, coordination.ErrInvalidLease) {
		t.Fatalf("expected zero-token release error, got %v", err)
	}
}

func TestLockerFromSharedClient(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	locker, err := NewLockerFromClient(client, "shared:")
	if err != nil {
		t.Fatal(err)
	}
	// Close must not close the caller-owned client.
	if err := locker.Close(); err != nil {
		t.Fatal(err)
	}
	lease, ok, err := locker.Acquire(ctx, "run:shared", "worker:1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("acquire via shared client failed: ok=%v err=%v", ok, err)
	}
	if got, err := client.Get(ctx, "shared:lock:run:shared").Result(); err != nil {
		t.Fatal(err)
	} else if got != lockValue("worker:1", lease.Token) {
		t.Fatalf("unexpected lock value %q", got)
	}
	if err := locker.Release(ctx, lease); err != nil {
		t.Fatal(err)
	}
}
