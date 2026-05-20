package agentruntime

import (
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	auditfile "github.com/aijustin/agentflow-go/internal/adapter/audit/file"
	"github.com/aijustin/agentflow-go/internal/adapter/auth/apikey"
	agentflow "github.com/aijustin/agentflow-go"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type Config struct {
	ScenarioFile string
	StateDir     string
	TokenSecret  string
	TokenTTL     time.Duration
	HTTPAddr     string
	QueueKind    string
	PostgresDSN  string
	APIKey       string
	TenantID     string
	PrincipalID  string
	AuditFile    string
	Version      string
	WorkerID     string
	Concurrency  int
	LeaseTTL     time.Duration
	RenewInterval time.Duration
	JobTimeout   time.Duration
	PollInterval time.Duration
}

func LoadConfigFromEnv() (Config, error) {
	getenv := os.Getenv
	scenarioFile := strings.TrimSpace(getenv("AGENT_SCENARIO_FILE"))
	if scenarioFile == "" {
		return Config{}, fmt.Errorf("AGENT_SCENARIO_FILE is required")
	}
	addr := strings.TrimSpace(getenv("AGENT_HTTP_ADDR"))
	if addr == "" {
		addr = "0.0.0.0:8080"
	}
	tokenSecret := strings.TrimSpace(getenv("AGENT_TOKEN_SECRET"))
	if tokenSecret == "" {
		if !IsLoopbackAddr(addr) {
			return Config{}, fmt.Errorf("AGENT_TOKEN_SECRET is required when AGENT_HTTP_ADDR is not loopback")
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
		if host == "" {
			host = "worker"
		}
		workerID = host
	}
	concurrency := 4
	if raw := strings.TrimSpace(getenv("AGENT_WORKER_CONCURRENCY")); raw != "" {
		var parsed int
		if _, err := fmt.Sscanf(raw, "%d", &parsed); err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("AGENT_WORKER_CONCURRENCY must be a positive integer")
		}
		concurrency = parsed
	}
	return Config{
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

func NewFramework(config Config, tokenWriter io.Writer) (*agentflow.Framework, error) {
	scenario, err := agentflow.LoadScenarioFile(config.ScenarioFile)
	if err != nil {
		return nil, err
	}
	workDir, err := agentflow.DemoWorkDir(config.ScenarioFile)
	if err != nil {
		return nil, err
	}
	opts := []agentflow.Option{
		agentflow.WithHITLTokenSecret([]byte(config.TokenSecret), tokenWriter),
		agentflow.WithHITLTokenTTL(config.TokenTTL),
	}
	if config.StateDir != "" {
		repo, err := agentflow.NewFileRunStateRepository(filepath.Join(config.StateDir, "runs"))
		if err != nil {
			return nil, err
		}
		blobs, err := agentflow.NewFileBlobStore(filepath.Join(config.StateDir, "blobs"))
		if err != nil {
			return nil, err
		}
		opts = append(opts, agentflow.WithRunStateRepository(repo), agentflow.WithBlobStore(blobs))
	}
	demoOpts, err := agentflow.DemoOptions(scenario, agentflow.DemoConfig{WorkDir: workDir})
	if err != nil {
		return nil, err
	}
	opts = append(opts, demoOpts...)
	return agentflow.New(scenario, opts...)
}

func NewQueue(config Config, db **sql.DB) (asyncpkg.Queue, error) {
	switch strings.ToLower(config.QueueKind) {
	case "memory", "inmemory", "in_memory":
		return agentflow.NewInMemoryJobQueue(), nil
	case "postgres":
		if config.PostgresDSN == "" {
			return nil, fmt.Errorf("AGENT_POSTGRES_DSN is required when AGENT_QUEUE=postgres")
		}
		opened, err := openPostgres(config.PostgresDSN)
		if err != nil {
			return nil, err
		}
		if db != nil {
			*db = opened
		}
		return agentflow.NewPostgresJobQueue(opened)
	default:
		return nil, fmt.Errorf("unsupported AGENT_QUEUE %q", config.QueueKind)
	}
}

func NewProductionHandler(config Config, fw *agentflow.Framework, queue asyncpkg.Queue) (http.Handler, error) {
	handlerConfig := agentflow.ProductionHTTPHandlerConfig{
		Queue:     queue,
		Policy:    security.NewDefaultRolePolicy(),
		Framework: fw,
		Version:   config.Version,
	}
	if config.APIKey != "" {
		authn, err := apiKeyMiddleware(config)
		if err != nil {
			return nil, err
		}
		handlerConfig.AuthMiddleware = authn
	} else if !IsLoopbackAddr(config.HTTPAddr) {
		return nil, fmt.Errorf("AGENT_HTTP_API_KEY is required when AGENT_HTTP_ADDR is not loopback")
	}
	if config.AuditFile != "" {
		sink, err := auditfile.NewSink(config.AuditFile)
		if err != nil {
			return nil, err
		}
		handlerConfig.Audit = sink
	}
	return agentflow.NewProductionHTTPHandler(handlerConfig)
}

func NewWorker(config Config, queue asyncpkg.Queue, fw *agentflow.Framework) (*asyncpkg.Worker, error) {
	jobHandler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		return nil, err
	}
	return asyncpkg.NewWorker(queue, jobHandler, asyncpkg.WorkerConfig{
		WorkerID:      config.WorkerID,
		Concurrency:   config.Concurrency,
		LeaseTTL:      config.LeaseTTL,
		RenewInterval: config.RenewInterval,
		JobTimeout:    config.JobTimeout,
		PollInterval:  config.PollInterval,
	})
}

func apiKeyMiddleware(config Config) (func(http.Handler) http.Handler, error) {
	authenticator, err := apikey.NewStaticAuthenticator(map[string]identity.Principal{
		config.APIKey: {
			ID:    config.PrincipalID,
			Type:  identity.PrincipalService,
			Scope: identity.Scope{TenantID: config.TenantID},
			Roles: []identity.Role{identity.RoleAdmin},
		},
	})
	if err != nil {
		return nil, err
	}
	return apikey.NewMiddleware(apikey.MiddlewareConfig{Authenticator: authenticator})
}

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
