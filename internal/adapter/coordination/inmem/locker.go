// Package inmem provides an in-process coordination.Locker for tests and
// single-process deployments. Semantics mirror the Redis locker: Acquire is
// first-writer-wins until the lease expires, every fresh Acquire mints a
// monotonically increasing fencing token for the key, and Renew/Release
// require the exact owner + token pair that holds the lease.
package inmem

import (
	"context"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/coordination"
)

type entry struct {
	owner     string
	token     uint64
	expiresAt time.Time
}

type Locker struct {
	mu     sync.Mutex
	leases map[string]entry
	// counters holds the last minted token per key. Like the Redis
	// fence:{key} counter it never expires and never resets, so tokens stay
	// monotonically increasing across release/re-acquire cycles.
	counters map[string]uint64
}

func NewLocker() *Locker {
	return &Locker{leases: make(map[string]entry), counters: make(map[string]uint64)}
}

func (l *Locker) Acquire(ctx context.Context, key string, owner string, ttl time.Duration) (coordination.Lease, bool, error) {
	if err := ctx.Err(); err != nil {
		return coordination.Lease{}, false, err
	}
	if key == "" || owner == "" || ttl <= 0 {
		return coordination.Lease{}, false, coordination.ErrInvalidLease
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UTC()
	if held, ok := l.leases[key]; ok && held.expiresAt.After(now) {
		if held.owner != owner {
			return coordination.Lease{}, false, nil
		}
		// Reentrant acquire by the current holder extends the expiry but
		// keeps the token: the holder identity is unchanged, so previously
		// handed-out handles must stay valid.
		expiresAt := now.Add(ttl)
		l.leases[key] = entry{owner: owner, token: held.token, expiresAt: expiresAt}
		return coordination.Lease{Key: key, Owner: owner, Token: held.token, ExpiresAt: expiresAt}, true, nil
	}
	l.counters[key]++
	token := l.counters[key]
	expiresAt := now.Add(ttl)
	l.leases[key] = entry{owner: owner, token: token, expiresAt: expiresAt}
	return coordination.Lease{Key: key, Owner: owner, Token: token, ExpiresAt: expiresAt}, true, nil
}

func (l *Locker) Renew(ctx context.Context, lease coordination.Lease, ttl time.Duration) (coordination.Lease, bool, error) {
	if err := ctx.Err(); err != nil {
		return coordination.Lease{}, false, err
	}
	if err := lease.Validate(); err != nil {
		return coordination.Lease{}, false, err
	}
	if ttl <= 0 {
		return coordination.Lease{}, false, coordination.ErrInvalidLease
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UTC()
	held, ok := l.leases[lease.Key]
	if !ok || held.owner != lease.Owner || held.token != lease.Token || !held.expiresAt.After(now) {
		return coordination.Lease{}, false, nil
	}
	expiresAt := now.Add(ttl)
	l.leases[lease.Key] = entry{owner: lease.Owner, token: lease.Token, expiresAt: expiresAt}
	lease.ExpiresAt = expiresAt
	return lease, true, nil
}

func (l *Locker) Release(ctx context.Context, lease coordination.Lease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := lease.Validate(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	held, ok := l.leases[lease.Key]
	if !ok || held.owner != lease.Owner || held.token != lease.Token {
		return coordination.ErrInvalidLease
	}
	delete(l.leases, lease.Key)
	return nil
}
