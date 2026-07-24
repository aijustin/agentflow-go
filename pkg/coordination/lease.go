package coordination

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidLease = errors.New("coordination: invalid lease")
	// ErrRunLeaseLost reports that the worker executing a run no longer holds
	// the run lease (renewal returned not-held or failed) and must stop
	// executing before another worker reaps or takes over the run. It is kept
	// in this package, not the framework facade, so the runtime engine can
	// classify it without an import cycle; the facade re-exports it.
	ErrRunLeaseLost = errors.New("agentflow: run lease lost")
)

// Lease is a held lock plus its fencing token. Token is assigned by the
// Locker on every successful Acquire and is monotonically increasing per key;
// Renew keeps the token unchanged. Downstream writers (e.g. fenced snapshot
// saves) compare the token against the highest token they have seen for the
// key and reject writes from superseded holders.
type Lease struct {
	Key       string
	Owner     string
	Token     uint64
	ExpiresAt time.Time
}

// Validate reports whether the lease is well-formed. A zero Token means the
// lease was hand-built or predates fencing and is rejected: only Locker
// implementations mint valid leases.
func (lease Lease) Validate() error {
	if lease.Key == "" || lease.Owner == "" || lease.Token == 0 {
		return ErrInvalidLease
	}
	return nil
}

type Locker interface {
	Acquire(ctx context.Context, key string, owner string, ttl time.Duration) (Lease, bool, error)
	Renew(ctx context.Context, lease Lease, ttl time.Duration) (Lease, bool, error)
	Release(ctx context.Context, lease Lease) error
}
