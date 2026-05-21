package agentflow

import (
	"database/sql"
	"net/http"
	"time"

	toolfilesystem "github.com/aijustin/agentflow-go/internal/adapter/tool/filesystem"
	toolhttp "github.com/aijustin/agentflow-go/internal/adapter/tool/http"
	toolsql "github.com/aijustin/agentflow-go/internal/adapter/tool/sqlquery"
	toolgit "github.com/aijustin/agentflow-go/internal/adapter/tool/git"
	toolticketadapter "github.com/aijustin/agentflow-go/internal/adapter/tool/ticket"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/toolticket"
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

type GitToolConfig struct {
	AllowedRoots []string
}

type TicketToolConfig struct {
	Store toolticket.Store
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

// NewGitToolExecutor creates a read-only git tool executor.
func NewGitToolExecutor(config GitToolConfig) (core.ToolExecutor, error) {
	return toolgit.NewExecutor(toolgit.Config{AllowedRoots: config.AllowedRoots})
}

// NewTicketToolExecutor creates a ticket store backed tool executor.
func NewTicketToolExecutor(config TicketToolConfig) (core.ToolExecutor, error) {
	return toolticketadapter.NewExecutor(config.Store)
}

// NewMemoryTicketStore creates an in-memory ticket store for tests and demos.
func NewMemoryTicketStore(seed map[string]Ticket) TicketStore {
	return toolticket.NewMemoryStore(seed)
}

// Ticket is a support ticket record manipulated by the ticket tool.
type Ticket = toolticket.Ticket

// TicketStore persists ticket records for the ticket tool executor.
type TicketStore = toolticket.Store
