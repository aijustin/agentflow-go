package agentflow

import (
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	auditfile "github.com/aijustin/agentflow-go/internal/adapter/audit/file"
	"github.com/aijustin/agentflow-go/internal/adapter/auth/apikey"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// ProductionConfig holds production wiring settings for library embedders.
type ProductionConfig struct {
	ScenarioFile  string
	StateDir      string
	TokenSecret   string
	TokenTTL      time.Duration
	HTTPAddr      string
	QueueKind     string
	PostgresDSN   string
	APIKey        string
	TenantID      string
	PrincipalID   string
	AuditFile     string
	Version       string
	WorkerID      string
	Concurrency   int
	LeaseTTL      time.Duration
	RenewInterval time.Duration
	JobTimeout    time.Duration
	PollInterval  time.Duration
}

// LoadProductionConfigFromEnv loads ProductionConfig from standard AGENT_* env vars.
func LoadProductionConfigFromEnv() (ProductionConfig, error) {
	getenv := os.Getenv
	scenarioFile := strings.TrimSpace(getenv("AGENT_SCENARIO_FILE"))
	if scenarioFile == "" {
		return ProductionConfig{}, fmt.Errorf("agentflow: AGENT_SCENARIO_FILE is required")
	}
	addr := strings.TrimSpace(getenv("AGENT_HTTP_ADDR"))
	if addr == "" {
		addr = "0.0.0.0:8080"
	}
	tokenSecret := strings.TrimSpace(getenv("AGENT_TOKEN_SECRET"))
	if tokenSecret == "" {
		if !IsLoopbackAddr(addr) {
			return ProductionConfig{}, fmt.Errorf("agentflow: AGENT_TOKEN_SECRET is required when AGENT_HTTP_ADDR is not loopback")
		}
		tokenSecret = "dev-secret"
	}
	queueKind := strings.TrimSpace(getenv("AGENT_QUEUE"))
	if queueKind == "" {
		if strings.TrimSpace(getenv("AGENT_POSTGRES_DSN")) != "" {
			queueKind = "postgres"
		} else {
			queueKind = "memory"
		}
	}
	tenantID := strings.TrimSpace(getenv("AGENT_HTTP_TENANT_ID"))
	if tenantID == "" {
		tenantID = "default"
	}
	principalID := strings.TrimSpace(getenv("AGENT_HTTP_PRINCIPAL_ID"))
	if principalID == "" {
		principalID = "agent-server"
	}
	workerID := strings.TrimSpace(getenv("AGENT_WORKER_ID"))
	if workerID == "" {
		host, _ := os.Hostname()
		if workerID = host; workerID == "" {
			workerID = "worker"
		}
	}
	concurrency := 4
	if raw := strings.TrimSpace(getenv("AGENT_WORKER_CONCURRENCY")); raw != "" {
		var parsed int
		if _, err := fmt.Sscanf(raw, "%d", &parsed); err != nil || parsed <= 0 {
			return ProductionConfig{}, fmt.Errorf("agentflow: AGENT_WORKER_CONCURRENCY must be a positive integer")
		}
		concurrency = parsed
	}
	return ProductionConfig{
		ScenarioFile:  scenarioFile,
		StateDir:      strings.TrimSpace(getenv("AGENT_STATE_DIR")),
		TokenSecret:   tokenSecret,
		TokenTTL:      durationFromEnv(getenv, "AGENT_TOKEN_TTL", 15*time.Minute),
		HTTPAddr:      addr,
		QueueKind:     queueKind,
		PostgresDSN:   strings.TrimSpace(getenv("AGENT_POSTGRES_DSN")),
		APIKey:        strings.TrimSpace(getenv("AGENT_HTTP_API_KEY")),
		TenantID:      tenantID,
		PrincipalID:   principalID,
		AuditFile:     strings.TrimSpace(getenv("AGENT_HTTP_AUDIT_FILE")),
		Version:       strings.TrimSpace(getenv("AGENT_VERSION")),
		WorkerID:      workerID,
		Concurrency:   concurrency,
		LeaseTTL:      durationFromEnv(getenv, "AGENT_WORKER_LEASE_TTL", time.Minute),
		RenewInterval: durationFromEnv(getenv, "AGENT_WORKER_RENEW_INTERVAL", 30*time.Second),
		JobTimeout:    durationFromEnv(getenv, "AGENT_WORKER_JOB_TIMEOUT", 2*time.Minute),
		PollInterval:  durationFromEnv(getenv, "AGENT_WORKER_POLL_INTERVAL", 100*time.Millisecond),
	}, nil
}

// ProductionOptions returns Framework options for a production-style deployment.
func ProductionOptions(cfg ProductionConfig, scenario core.Scenario, workDir string) ([]Option, error) {
	opts := []Option{
		WithHITLTokenSecret([]byte(cfg.TokenSecret), os.Stderr),
		WithHITLTokenTTL(cfg.TokenTTL),
	}
	stateOpts, err := developmentStateOptions(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	opts = append(opts, stateOpts...)
	devOpts, err := DevelopmentOptions(scenario, DevelopmentConfig{WorkDir: workDir})
	if err != nil {
		return nil, err
	}
	opts = append(opts, devOpts...)
	return opts, nil
}

// NewProduction constructs a Framework from ProductionConfig.
func NewProduction(cfg ProductionConfig, tokenWriter io.Writer) (*Framework, error) {
	scenario, err := LoadScenarioFile(cfg.ScenarioFile)
	if err != nil {
		return nil, err
	}
	workDir, err := DemoWorkDir(cfg.ScenarioFile)
	if err != nil {
		return nil, err
	}
	opts := []Option{
		WithHITLTokenSecret([]byte(cfg.TokenSecret), tokenWriter),
		WithHITLTokenTTL(cfg.TokenTTL),
	}
	stateOpts, err := developmentStateOptions(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	opts = append(opts, stateOpts...)
	devOpts, err := DevelopmentOptions(scenario, DevelopmentConfig{WorkDir: workDir})
	if err != nil {
		return nil, err
	}
	opts = append(opts, devOpts...)
	return New(scenario, opts...)
}

// NewProductionQueue creates a job queue from ProductionConfig.
func NewProductionQueue(cfg ProductionConfig, db **sql.DB) (asyncpkg.Queue, error) {
	switch strings.ToLower(cfg.QueueKind) {
	case "memory", "inmemory", "in_memory":
		return NewInMemoryJobQueue(), nil
	case "postgres":
		if cfg.PostgresDSN == "" {
			return nil, fmt.Errorf("agentflow: AGENT_POSTGRES_DSN is required when AGENT_QUEUE=postgres")
		}
		opened, err := openPostgres(cfg.PostgresDSN)
		if err != nil {
			return nil, err
		}
		if db != nil {
			*db = opened
		}
		return NewPostgresJobQueue(opened)
	default:
		return nil, fmt.Errorf("agentflow: unsupported queue kind %q", cfg.QueueKind)
	}
}

// BuildProductionHTTPHandler builds the production HTTP API handler from env-style config.
func BuildProductionHTTPHandler(cfg ProductionConfig, fw *Framework, queue asyncpkg.Queue) (http.Handler, error) {
	handlerConfig := ProductionHTTPHandlerConfig{
		Queue:     queue,
		Policy:    security.NewDefaultRolePolicy(),
		Framework: fw,
		Version:   cfg.Version,
	}
	if cfg.APIKey != "" {
		authn, err := productionAPIKeyMiddleware(cfg)
		if err != nil {
			return nil, err
		}
		handlerConfig.AuthMiddleware = authn
	} else if !IsLoopbackAddr(cfg.HTTPAddr) {
		return nil, fmt.Errorf("agentflow: AGENT_HTTP_API_KEY is required when AGENT_HTTP_ADDR is not loopback")
	}
	if cfg.AuditFile != "" {
		sink, err := auditfile.NewSink(cfg.AuditFile)
		if err != nil {
			return nil, err
		}
		handlerConfig.Audit = sink
	}
	return NewProductionHTTPHandler(handlerConfig)
}

// NewProductionWorker creates an async worker for production job processing.
func NewProductionWorker(cfg ProductionConfig, queue asyncpkg.Queue, fw *Framework) (*asyncpkg.Worker, error) {
	jobHandler, err := NewFrameworkJobHandler(FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		return nil, err
	}
	return asyncpkg.NewWorker(queue, jobHandler, asyncpkg.WorkerConfig{
		WorkerID:      cfg.WorkerID,
		Concurrency:   cfg.Concurrency,
		LeaseTTL:      cfg.LeaseTTL,
		RenewInterval: cfg.RenewInterval,
		JobTimeout:    cfg.JobTimeout,
		PollInterval:  cfg.PollInterval,
	})
}

// IsLoopbackAddr reports whether an HTTP listen address is loopback-only.
func IsLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func openPostgres(dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("agentflow: open postgres: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("agentflow: ping postgres: %w", err)
	}
	return db, nil
}

func productionAPIKeyMiddleware(cfg ProductionConfig) (func(http.Handler) http.Handler, error) {
	authenticator, err := apikey.NewStaticAuthenticator(map[string]identity.Principal{
		cfg.APIKey: {
			ID:    cfg.PrincipalID,
			Type:  identity.PrincipalService,
			Scope: identity.Scope{TenantID: cfg.TenantID},
			Roles: []identity.Role{identity.RoleAdmin},
		},
	})
	if err != nil {
		return nil, err
	}
	return apikey.NewMiddleware(apikey.MiddlewareConfig{Authenticator: authenticator})
}

func durationFromEnv(getenv func(string) string, key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return parsed
}
