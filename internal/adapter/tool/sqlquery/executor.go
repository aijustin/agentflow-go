package sqlquery

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

const DefaultMaxRows = 100

const DefaultTimeout = 5 * time.Second

type Config struct {
	DB              *sql.DB
	AllowedQueries  map[string]string
	AllowAdHocQuery bool
	MaxRows         int
	Timeout         time.Duration
}

type Executor struct {
	db              *sql.DB
	allowedQueries  map[string]string
	allowAdHocQuery bool
	maxRows         int
	timeout         time.Duration
}

type Request struct {
	QueryID string `json:"query_id,omitempty"`
	Query   string `json:"query,omitempty"`
	Args    []any  `json:"args,omitempty"`
}

type Response struct {
	QueryID   string           `json:"query_id,omitempty"`
	Columns   []string         `json:"columns"`
	Rows      []map[string]any `json:"rows"`
	RowCount  int              `json:"row_count"`
	Truncated bool             `json:"truncated,omitempty"`
}

func NewExecutor(config Config) (*Executor, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("sql tool: db is nil")
	}
	allowedQueries := make(map[string]string, len(config.AllowedQueries))
	for id, query := range config.AllowedQueries {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("sql tool: query id is required")
		}
		if err := validateReadOnlyQuery(query); err != nil {
			return nil, fmt.Errorf("sql tool: invalid allowed query %q: %w", id, err)
		}
		allowedQueries[id] = strings.TrimSpace(query)
	}
	maxRows := config.MaxRows
	if maxRows <= 0 {
		maxRows = DefaultMaxRows
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Executor{db: config.DB, allowedQueries: allowedQueries, allowAdHocQuery: config.AllowAdHocQuery, maxRows: maxRows, timeout: timeout}, nil
}

func (executor *Executor) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolResult{}, err
	}
	if len(call.Input) == 0 {
		return core.ToolResult{}, fmt.Errorf("sql tool: input is required")
	}
	var input Request
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return core.ToolResult{}, fmt.Errorf("sql tool: decode input: %w", err)
	}
	query, queryID, err := executor.resolveQuery(input)
	if err != nil {
		return core.ToolResult{}, err
	}
	if err := validateReadOnlyQuery(query); err != nil {
		return core.ToolResult{}, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, executor.timeout)
	defer cancel()
	rows, err := executor.db.QueryContext(queryCtx, query, input.Args...)
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("sql tool: query: %w", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("sql tool: columns: %w", err)
	}
	response := Response{QueryID: queryID, Columns: columns, Rows: make([]map[string]any, 0)}
	for rows.Next() {
		if len(response.Rows) >= executor.maxRows {
			response.Truncated = true
			break
		}
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return core.ToolResult{}, fmt.Errorf("sql tool: scan row: %w", err)
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			row[column] = normalizeValue(values[i])
		}
		response.Rows = append(response.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return core.ToolResult{}, fmt.Errorf("sql tool: rows: %w", err)
	}
	response.RowCount = len(response.Rows)
	output, err := json.Marshal(response)
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{Tool: call.Tool, Output: output}, nil
}

func (executor *Executor) resolveQuery(input Request) (query string, queryID string, err error) {
	queryID = strings.TrimSpace(input.QueryID)
	if queryID != "" {
		query, ok := executor.allowedQueries[queryID]
		if !ok {
			return "", "", fmt.Errorf("sql tool: query id %q is not allowed", queryID)
		}
		return query, queryID, nil
	}
	query = strings.TrimSpace(input.Query)
	if query == "" {
		return "", "", fmt.Errorf("sql tool: query_id is required")
	}
	if !executor.allowAdHocQuery {
		return "", "", fmt.Errorf("sql tool: ad-hoc queries are disabled")
	}
	return query, "", nil
}

func normalizeValue(value any) any {
	switch typed := value.(type) {
	case []byte:
		return string(typed)
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	default:
		return typed
	}
}

func validateReadOnlyQuery(query string) error {
	tokens, hasTerminator, err := sqlTokens(query)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		return fmt.Errorf("sql tool: query is required")
	}
	if hasTerminator {
		return fmt.Errorf("sql tool: multiple statements are not allowed")
	}
	if tokens[0] != "SELECT" && tokens[0] != "WITH" {
		return fmt.Errorf("sql tool: only SELECT queries are allowed")
	}
	if tokens[0] == "WITH" && !containsToken(tokens, "SELECT") {
		return fmt.Errorf("sql tool: WITH queries must contain SELECT")
	}
	for _, token := range tokens {
		switch token {
		case "INSERT", "UPDATE", "DELETE", "DROP", "ALTER", "CREATE", "TRUNCATE", "MERGE", "CALL", "EXEC", "EXECUTE", "GRANT", "REVOKE", "COPY", "VACUUM", "ANALYZE", "SET", "RESET", "BEGIN", "COMMIT", "ROLLBACK", "LOCK", "INTO", "OUTFILE", "DUMPFILE":
			return fmt.Errorf("sql tool: mutating keyword %q is not allowed", token)
		}
	}
	return nil
}

func containsToken(tokens []string, want string) bool {
	for _, token := range tokens {
		if token == want {
			return true
		}
	}
	return false
}

func sqlTokens(query string) ([]string, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, false, nil
	}
	tokens := make([]string, 0)
	var builder strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	inBacktickQuote := false
	inLineComment := false
	inBlockComment := false
	hasTerminator := false
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		tokens = append(tokens, strings.ToUpper(builder.String()))
		builder.Reset()
	}
	for i := 0; i < len(query); i++ {
		ch := query[i]
		next := byte(0)
		if i+1 < len(query) {
			next = query[i+1]
		}
		switch {
		case inLineComment:
			if ch == '\n' || ch == '\r' {
				inLineComment = false
			}
			continue
		case inBlockComment:
			if ch == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		case inSingleQuote:
			if ch == '\'' {
				if next == '\'' {
					i++
					continue
				}
				inSingleQuote = false
			}
			continue
		case inDoubleQuote:
			if ch == '"' {
				if next == '"' {
					i++
					continue
				}
				inDoubleQuote = false
			}
			continue
		case inBacktickQuote:
			if ch == '`' {
				if next == '`' {
					i++
					continue
				}
				inBacktickQuote = false
			}
			continue
		case ch == '-' && next == '-':
			flush()
			inLineComment = true
			i++
			continue
		case ch == '#':
			flush()
			inLineComment = true
			continue
		case ch == '/' && next == '*':
			flush()
			inBlockComment = true
			i++
			continue
		case ch == '\'':
			flush()
			inSingleQuote = true
			continue
		case ch == '"':
			flush()
			inDoubleQuote = true
			continue
		case ch == '`':
			flush()
			inBacktickQuote = true
			continue
		case ch == ';':
			flush()
			hasTerminator = true
			continue
		case isTokenChar(ch):
			builder.WriteByte(ch)
		default:
			flush()
		}
	}
	if inSingleQuote || inDoubleQuote || inBacktickQuote || inBlockComment {
		return nil, false, fmt.Errorf("sql tool: unterminated query literal or comment")
	}
	flush()
	return tokens, hasTerminator, nil
}

func isTokenChar(ch byte) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || ch == '_'
}
