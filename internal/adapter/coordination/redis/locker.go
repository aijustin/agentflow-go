package redis

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aijustin/agentflow-go/pkg/coordination"
)

const (
	defaultDialTimeout  = 5 * time.Second
	defaultReadTimeout  = 5 * time.Second
	defaultWriteTimeout = 5 * time.Second

	renewScript   = `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("PEXPIRE", KEYS[1], ARGV[2]) else return 0 end`
	releaseScript = `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`
)

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9._:/=-]+$`)

type DialFunc func(ctx context.Context, network string, address string) (net.Conn, error)

type Config struct {
	Addr         string
	Password     string
	DB           int
	KeyPrefix    string
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	DialContext  DialFunc
}

type Locker struct {
	addr         string
	password     string
	db           int
	keyPrefix    string
	dialTimeout  time.Duration
	readTimeout  time.Duration
	writeTimeout time.Duration
	dialContext  DialFunc
}

func NewLocker(config Config) (*Locker, error) {
	if config.Addr == "" {
		return nil, fmt.Errorf("redis coordination: address is required")
	}
	if config.DB < 0 {
		return nil, fmt.Errorf("redis coordination: db must be >= 0")
	}
	dialTimeout := config.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = defaultDialTimeout
	}
	readTimeout := config.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = defaultReadTimeout
	}
	writeTimeout := config.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = defaultWriteTimeout
	}
	dialContext := config.DialContext
	if dialContext == nil {
		dialer := net.Dialer{Timeout: dialTimeout}
		dialContext = dialer.DialContext
	}
	return &Locker{
		addr:         config.Addr,
		password:     config.Password,
		db:           config.DB,
		keyPrefix:    config.KeyPrefix,
		dialTimeout:  dialTimeout,
		readTimeout:  readTimeout,
		writeTimeout: writeTimeout,
		dialContext:  dialContext,
	}, nil
}

func (locker *Locker) Acquire(ctx context.Context, key string, owner string, ttl time.Duration) (coordination.Lease, bool, error) {
	if err := validateLeaseInput(key, owner, ttl); err != nil {
		return coordination.Lease{}, false, err
	}
	millis := ttlMillis(ttl)
	value, err := locker.do(ctx, "SET", locker.redisKey(key), owner, "NX", "PX", strconv.FormatInt(millis, 10))
	if err != nil {
		return coordination.Lease{}, false, err
	}
	if value.nil {
		return coordination.Lease{}, false, nil
	}
	if value.simple != "OK" {
		return coordination.Lease{}, false, fmt.Errorf("redis coordination: unexpected SET response")
	}
	return coordination.Lease{Key: key, Owner: owner, ExpiresAt: time.Now().UTC().Add(ttl)}, true, nil
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
	millis := ttlMillis(ttl)
	value, err := locker.do(ctx, "EVAL", renewScript, "1", locker.redisKey(lease.Key), lease.Owner, strconv.FormatInt(millis, 10))
	if err != nil {
		return coordination.Lease{}, false, err
	}
	if value.integer != 1 {
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
	value, err := locker.do(ctx, "EVAL", releaseScript, "1", locker.redisKey(lease.Key), lease.Owner)
	if err != nil {
		return err
	}
	if value.integer != 1 {
		return coordination.ErrInvalidLease
	}
	return nil
}

func (locker *Locker) do(ctx context.Context, args ...string) (respValue, error) {
	conn, err := locker.dialContext(ctx, "tcp", locker.addr)
	if err != nil {
		return respValue{}, fmt.Errorf("redis coordination: dial: %w", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	if locker.password != "" {
		if _, err := locker.roundTrip(ctx, conn, reader, "AUTH", locker.password); err != nil {
			return respValue{}, err
		}
	}
	if locker.db > 0 {
		if _, err := locker.roundTrip(ctx, conn, reader, "SELECT", strconv.Itoa(locker.db)); err != nil {
			return respValue{}, err
		}
	}
	return locker.roundTrip(ctx, conn, reader, args...)
}

func (locker *Locker) roundTrip(ctx context.Context, conn net.Conn, reader *bufio.Reader, args ...string) (respValue, error) {
	if err := ctx.Err(); err != nil {
		return respValue{}, err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(locker.writeTimeout)); err != nil {
		return respValue{}, err
	}
	if err := writeCommand(conn, args...); err != nil {
		return respValue{}, fmt.Errorf("redis coordination: write command: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(locker.readTimeout)); err != nil {
		return respValue{}, err
	}
	return readValue(reader)
}

func (locker *Locker) redisKey(key string) string {
	return locker.keyPrefix + key
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

func writeCommand(writer io.Writer, args ...string) error {
	if _, err := fmt.Fprintf(writer, "*%d\r\n", len(args)); err != nil {
		return err
	}
	for _, arg := range args {
		if _, err := fmt.Fprintf(writer, "$%d\r\n%s\r\n", len(arg), arg); err != nil {
			return err
		}
	}
	return nil
}

type respValue struct {
	simple  string
	integer int64
	bulk    string
	nil     bool
}

func readValue(reader *bufio.Reader) (respValue, error) {
	prefix, err := reader.ReadByte()
	if err != nil {
		return respValue{}, err
	}
	switch prefix {
	case '+':
		line, err := readLine(reader)
		return respValue{simple: line}, err
	case '-':
		line, err := readLine(reader)
		if err != nil {
			return respValue{}, err
		}
		return respValue{}, fmt.Errorf("redis coordination: %s", line)
	case ':':
		line, err := readLine(reader)
		if err != nil {
			return respValue{}, err
		}
		integer, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			return respValue{}, err
		}
		return respValue{integer: integer}, nil
	case '$':
		line, err := readLine(reader)
		if err != nil {
			return respValue{}, err
		}
		length, err := strconv.Atoi(line)
		if err != nil {
			return respValue{}, err
		}
		if length == -1 {
			return respValue{nil: true}, nil
		}
		data := make([]byte, length+2)
		if _, err := io.ReadFull(reader, data); err != nil {
			return respValue{}, err
		}
		return respValue{bulk: string(data[:length])}, nil
	default:
		return respValue{}, fmt.Errorf("redis coordination: unsupported response prefix %q", prefix)
	}
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}
