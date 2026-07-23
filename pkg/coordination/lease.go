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

type Lease struct {
	Key       string
	Owner     string
	ExpiresAt time.Time
}

func (lease Lease) Validate() error {
	if lease.Key == "" || lease.Owner == "" {
		return ErrInvalidLease
	}
	return nil
}

type Locker interface {
	Acquire(ctx context.Context, key string, owner string, ttl time.Duration) (Lease, bool, error)
	Renew(ctx context.Context, lease Lease, ttl time.Duration) (Lease, bool, error)
	Release(ctx context.Context, lease Lease) error
}
