package agentflow

import (
	"database/sql"
	"net/http"
	"time"

	toolfilesystem "github.com/aijustin/agentflow-go/internal/adapter/tool/filesystem"
	toolhttp "github.com/aijustin/agentflow-go/internal/adapter/tool/http"
	toolsql "github.com/aijustin/agentflow-go/internal/adapter/tool/sqlquery"
	"github.com/aijustin/agentflow-go/pkg/core"
)

type ToolResolver = core.ToolResolver
type ToolResolverFunc = core.ToolResolverFunc

type HTTPToolConfig struct {
	AllowedHosts     []string
	AllowedMethods   []string
	DefaultHeaders   map[string]string
	MaxResponseBytes int64
	Client           *http.Client
}

type FilesystemToolConfig struct {
	AllowedRoots []string
	MaxBytes     int64
}

type SQLToolConfig struct {
	DB              *sql.DB
	AllowedQueries  map[string]string
	AllowAdHocQuery bool
	MaxRows         int
	Timeout         time.Duration
}

// NewHTTPToolExecutor creates a governed HTTP client tool executor.
func NewHTTPToolExecutor(config HTTPToolConfig) (core.ToolExecutor, error) {
	return toolhttp.NewExecutor(toolhttp.Config{
		AllowedHosts:     config.AllowedHosts,
		AllowedMethods:   config.AllowedMethods,
		DefaultHeaders:   config.DefaultHeaders,
		MaxResponseBytes: config.MaxResponseBytes,
		Client:           config.Client,
	})
}

// NewFilesystemToolExecutor creates a governed filesystem read tool executor.
func NewFilesystemToolExecutor(config FilesystemToolConfig) (core.ToolExecutor, error) {
	return toolfilesystem.NewExecutor(toolfilesystem.Config{
		AllowedRoots: config.AllowedRoots,
		MaxBytes:     config.MaxBytes,
	})
}

// NewSQLToolExecutor creates a governed read-only SQL query tool executor.
func NewSQLToolExecutor(config SQLToolConfig) (core.ToolExecutor, error) {
	return toolsql.NewExecutor(toolsql.Config{
		DB:              config.DB,
		AllowedQueries:  config.AllowedQueries,
		AllowAdHocQuery: config.AllowAdHocQuery,
		MaxRows:         config.MaxRows,
		Timeout:         config.Timeout,
	})
}
