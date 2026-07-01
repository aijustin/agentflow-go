// Package inmem provides an in-process coordination.Locker for tests and
// single-process deployments. Semantics mirror the Redis locker: Acquire is
// first-writer-wins until the lease expires, Renew and Release require the
// holding owner.
package inmem

import (
	"context"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/coordination"
)

type entry struct {
	owner     string
	expiresAt time.Time
}

type Locker struct {
	mu     sync.Mutex
	leases map[string]entry
}

func NewLocker() *Locker {
	return &Locker{leases: make(map[string]entry)}
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
	if held, ok := l.leases[key]; ok && held.expiresAt.After(now) && held.owner != owner {
		return coordination.Lease{}, false, nil
	}
	expiresAt := now.Add(ttl)
	l.leases[key] = entry{owner: owner, expiresAt: expiresAt}
	return coordination.Lease{Key: key, Owner: owner, ExpiresAt: expiresAt}, true, nil
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
	if !ok || held.owner != lease.Owner || !held.expiresAt.After(now) {
		return coordination.Lease{}, false, nil
	}
	expiresAt := now.Add(ttl)
	l.leases[lease.Key] = entry{owner: lease.Owner, expiresAt: expiresAt}
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
	if !ok || held.owner != lease.Owner {
		return coordination.ErrInvalidLease
	}
	delete(l.leases, lease.Key)
	return nil
}
