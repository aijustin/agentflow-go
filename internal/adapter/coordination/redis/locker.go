// Package redis provides a coordination.Locker backed by Redis with
// monotonically increasing fencing tokens. Every successful Acquire mints a
// new token (per key) and stores it inside the lock value as
// "{owner}:{token}"; Renew and Release compare the full value, so a stale
// handle from a superseded holder — including one from a different process
// configured with the same owner name — is rejected instead of silently
// extending or deleting the new holder's lock.
package redis

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/aijustin/agentflow-go/pkg/coordination"
)

const (
	defaultDialTimeout  = 5 * time.Second
	defaultReadTimeout  = 5 * time.Second
	defaultWriteTimeout = 5 * time.Second

	// acquireScript atomically checks the lock is free, mints the next
	// fencing token, and stores it in the lock value. Redis executes Lua
	// scripts to completion without interleaving other commands, so the
	// GET → INCR → SET sequence is a compare-and-set: the token is only
	// incremented when the lock was observed free inside the same atomic
	// step, and the value written always equals the token returned. No
	// client can slip between INCR and SET, so the lock value and the
	// fence counter can never diverge. Returns 0 when the lock is held.
	acquireScript = `
if redis.call('GET', KEYS[1]) then
  return 0
end
local token = redis.call('INCR', KEYS[2])
redis.call('SET', KEYS[1], ARGV[1] .. ':' .. token, 'PX', ARGV[2])
return token
`

	renewScript   = `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("PEXPIRE", KEYS[1], ARGV[2]) else return 0 end`
	releaseScript = `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`
)

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9._:/=-]+$`)

type Config struct {
	Addr         string
	Password     string
	DB           int
	KeyPrefix    string
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type Locker struct {
	client    *redis.Client
	keyPrefix string
	// ownsClient reports whether Close should close the client; clients
	// injected via NewLockerFromClient are owned by the caller.
	ownsClient bool
}

// NewLocker creates a pooled Redis locker from connection settings. The
// underlying go-redis client maintains a connection pool; call Close when the
// locker is no longer needed.
func NewLocker(config Config) (*Locker, error) {
	if config.Addr == "" {
		return nil, fmt.Errorf("redis coordination: address is required")
	}
	if config.DB < 0 {
		return nil, fmt.Errorf("redis coordination: db must be >= 0")
	}
	client := redis.NewClient(&redis.Options{
		Addr:         config.Addr,
		Password:     config.Password,
		DB:           config.DB,
		DialTimeout:  firstDuration(config.DialTimeout, defaultDialTimeout),
		ReadTimeout:  firstDuration(config.ReadTimeout, defaultReadTimeout),
		WriteTimeout: firstDuration(config.WriteTimeout, defaultWriteTimeout),
	})
	return &Locker{client: client, keyPrefix: config.KeyPrefix, ownsClient: true}, nil
}

// NewLockerFromClient wraps an existing (pooled) go-redis client so several
// subsystems can share one connection pool. The client is NOT closed by
// Close; its lifecycle stays with the caller.
func NewLockerFromClient(client *redis.Client, keyPrefix string) (*Locker, error) {
	if client == nil {
		return nil, fmt.Errorf("redis coordination: client is nil")
	}
	return &Locker{client: client, keyPrefix: keyPrefix}, nil
}

// Close releases the connection pool owned by this locker. It is a no-op for
// lockers wrapping a caller-provided client.
func (locker *Locker) Close() error {
	if locker.ownsClient {
		return locker.client.Close()
	}
	return nil
}

func (locker *Locker) Acquire(ctx context.Context, key string, owner string, ttl time.Duration) (coordination.Lease, bool, error) {
	if err := validateLeaseInput(key, owner, ttl); err != nil {
		return coordination.Lease{}, false, err
	}
	token, err := locker.client.Eval(ctx, acquireScript,
		[]string{locker.lockKey(key), locker.fenceKey(key)},
		owner, ttlMillis(ttl),
	).Int64()
	if err != nil {
		return coordination.Lease{}, false, fmt.Errorf("redis coordination: acquire %q: %w", key, err)
	}
	if token == 0 {
		return coordination.Lease{}, false, nil
	}
	return coordination.Lease{Key: key, Owner: owner, Token: uint64(token), ExpiresAt: time.Now().UTC().Add(ttl)}, true, nil
}

func (locker *Locker) Renew(ctx context.Context, lease coordination.Lease, ttl time.Duration) (coordination.Lease, bool, error) {
	if err := lease.Validate(); err != nil {
		return coordination.Lease{}, false, err
	}
	if ttl <= 0 {
		return coordination.Lease{}, false, coordination.ErrInvalidLease
	}
	if !validToken(lease.Key) || !validToken(lease.Owner) {
		return coordination.Lease{}, false, coordination.ErrInvalidLease
	}
	held, err := locker.client.Eval(ctx, renewScript,
		[]string{locker.lockKey(lease.Key)},
		lockValue(lease.Owner, lease.Token), ttlMillis(ttl),
	).Int64()
	if err != nil {
		return coordination.Lease{}, false, fmt.Errorf("redis coordination: renew %q: %w", lease.Key, err)
	}
	if held != 1 {
		return coordination.Lease{}, false, nil
	}
	lease.ExpiresAt = time.Now().UTC().Add(ttl)
	return lease, true, nil
}

func (locker *Locker) Release(ctx context.Context, lease coordination.Lease) error {
	if err := lease.Validate(); err != nil {
		return err
	}
	if !validToken(lease.Key) || !validToken(lease.Owner) {
		return coordination.ErrInvalidLease
	}
	released, err := locker.client.Eval(ctx, releaseScript,
		[]string{locker.lockKey(lease.Key)},
		lockValue(lease.Owner, lease.Token),
	).Int64()
	if err != nil {
		return fmt.Errorf("redis coordination: release %q: %w", lease.Key, err)
	}
	if released != 1 {
		return coordination.ErrInvalidLease
	}
	return nil
}

func (locker *Locker) lockKey(key string) string {
	return locker.keyPrefix + "lock:" + key
}

// fenceKey deliberately has no expiry: the token must stay monotonically
// increasing for as long as the lock keyspace lives. A fence counter reset
// would let a new holder reuse an old token; the durable backstop against
// wholesale Redis data loss is the fence_token column in PostgreSQL.
func (locker *Locker) fenceKey(key string) string {
	return locker.keyPrefix + "fence:" + key
}

func lockValue(owner string, token uint64) string {
	return owner + ":" + strconv.FormatUint(token, 10)
}

func validateLeaseInput(key string, owner string, ttl time.Duration) error {
	if ttl <= 0 || !validToken(key) || !validToken(owner) {
		return coordination.ErrInvalidLease
	}
	return nil
}

func validToken(value string) bool {
	return value != "" && keyPattern.MatchString(value)
}

func ttlMillis(ttl time.Duration) int64 {
	millis := int64((ttl + time.Millisecond - 1) / time.Millisecond)
	if millis < 1 {
		return 1
	}
	return millis
}

func firstDuration(value time.Duration, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}
