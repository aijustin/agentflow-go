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
	MetricsHandler http.Handler
	IDGenerator    func() string
	Now            func() time.Time
	MaxBodyBytes   int64
	Version        string
	// Framework enables sync /v1/events and /v1/hitl/resume when set.
	Framework *Framework
	// StudioSavePath enables POST /v1/studio/save for the configured scenario file.
	StudioSavePath string
}

func NewProductionHTTPHandler(config ProductionHTTPHandlerConfig) (http.Handler, error) {
	apiConfig := apihttp.HandlerConfig{
		Queue:          config.Queue,
		Policy:         config.Policy,
		Audit:          config.Audit,
		AuthMiddleware: config.AuthMiddleware,
		MetricsHandler: config.MetricsHandler,
		IDGenerator:    config.IDGenerator,
		Now:            config.Now,
		MaxBodyBytes:   config.MaxBodyBytes,
		Version:        config.Version,
	}
	if config.Framework != nil {
		eventsHandler, err := NewWebhookHTTPHandler(WebhookHTTPHandlerConfig{
			Framework:    config.Framework,
			MaxBodyBytes: config.MaxBodyBytes,
		})
		if err != nil {
			return nil, err
		}
		apiConfig.EventsHandler = eventsHandler
		apiConfig.HITLHandler = NewHumanHTTPHandler(HumanHTTPHandlerConfig{
			Framework:    config.Framework,
			MaxBodyBytes: config.MaxBodyBytes,
		})
		checkpointHandler, err := NewCheckpointHTTPHandler(CheckpointHTTPHandlerConfig{
			Framework:    config.Framework,
			MaxBodyBytes: config.MaxBodyBytes,
		})
		if err != nil {
			return nil, err
		}
		apiConfig.CheckpointHandler = checkpointHandler
		studioHandler, err := NewStudioHTTPHandler(StudioHTTPHandlerConfig{
			Framework:      config.Framework,
			StudioSavePath: config.StudioSavePath,
			MaxBodyBytes:   config.MaxBodyBytes,
		})
		if err != nil {
			return nil, err
		}
		apiConfig.StudioHandler = studioHandler
	}
	return apihttp.NewHandler(apiConfig)
}
