package agentflow

import (
	"time"

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
