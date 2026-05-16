package agentflow

import (
	"net/http"
	"time"

	apihttp "github.com/aijustin/agentflow-go/internal/adapter/api/http"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type ProductionHTTPHandlerConfig struct {
	Queue          asyncpkg.Queue
	Policy         security.Policy
	Audit          audit.Sink
	AuthMiddleware func(http.Handler) http.Handler
	IDGenerator    func() string
	Now            func() time.Time
	MaxBodyBytes   int64
	Version        string
}

func NewProductionHTTPHandler(config ProductionHTTPHandlerConfig) (http.Handler, error) {
	return apihttp.NewHandler(apihttp.HandlerConfig{
		Queue:          config.Queue,
		Policy:         config.Policy,
		Audit:          config.Audit,
		AuthMiddleware: config.AuthMiddleware,
		IDGenerator:    config.IDGenerator,
		Now:            config.Now,
		MaxBodyBytes:   config.MaxBodyBytes,
		Version:        config.Version,
	})
}
