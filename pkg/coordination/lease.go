package coordination

import (
	"context"
	"errors"
	"time"
)

var ErrInvalidLease = errors.New("coordination: invalid lease")

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
