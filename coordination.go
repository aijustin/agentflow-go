package agentflow

import (
	"time"

	inmemcoord "github.com/aijustin/agentflow-go/internal/adapter/coordination/inmem"
	rediscoord "github.com/aijustin/agentflow-go/internal/adapter/coordination/redis"
	"github.com/aijustin/agentflow-go/pkg/coordination"
)

type RedisLockerConfig struct {
	Addr         string
	Password     string
	DB           int
	KeyPrefix    string
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// NewInMemoryLocker creates an in-process lease manager for tests and
// single-process deployments.
func NewInMemoryLocker() coordination.Locker {
	return inmemcoord.NewLocker()
}

// NewRedisLocker creates a Redis-backed lease manager for distributed worker
// and workflow coordination.
func NewRedisLocker(config RedisLockerConfig) (coordination.Locker, error) {
	return rediscoord.NewLocker(rediscoord.Config{
		Addr:         config.Addr,
		Password:     config.Password,
		DB:           config.DB,
		KeyPrefix:    config.KeyPrefix,
		DialTimeout:  config.DialTimeout,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
	})
}
