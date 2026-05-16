# SQL Tool Executor

`agentflow.NewSQLToolExecutor` exposes constrained read-only SQL queries as a normal `core.ToolExecutor`. It is intended for operational dashboards, ticket metadata, reporting tables, and other low-risk lookup flows where an agent needs database-backed context.

## Safety Model

The executor is deny-by-default:

- A `*sql.DB` must be provided by the host application.
- Named `AllowedQueries` are preferred and are validated at construction time.
- Ad-hoc SQL is disabled unless `AllowAdHocQuery` is explicitly enabled.
- Only `SELECT` queries are accepted.
- Statement terminators are rejected to avoid multi-statement execution.
- Obvious mutating SQL keywords such as `INSERT`, `UPDATE`, `DELETE`, `DROP`, and `ALTER` are rejected outside string literals and comments.
- Query execution uses `QueryContext` with a timeout. The default timeout is 5 seconds.
- Returned rows are capped. The default limit is 100 rows.
- Results are structured JSON with columns, rows, row count, and truncation status.

Use a database role with read-only permissions and least-privilege table access. The executor is a guardrail around the tool boundary, not a substitute for database authorization.

## Wiring

The SQL tool is database-driver neutral. The framework accepts a `*sql.DB` and does not import concrete drivers, so the host application chooses the driver and DSN. Query placeholders are not rewritten; use the placeholder syntax expected by the selected driver.

```go
sqlTool, err := agentflow.NewSQLToolExecutor(agentflow.SQLToolConfig{
  DB: db,
  AllowedQueries: map[string]string{
    "tickets.by_status": "SELECT id, title, status FROM tickets WHERE status = $1 ORDER BY created_at DESC",
  },
  MaxRows: 20,
})
if err != nil {
  log.Fatal(err)
}

fw, err := agentflow.NewFromFile(
  "scenario.yaml",
  agentflow.WithToolExecutor("sql.query", sqlTool),
)
```

### MySQL

```go
import (
  "database/sql"

  _ "github.com/go-sql-driver/mysql"
)

db, err := sql.Open("mysql", os.Getenv("AGENTFLOW_MYSQL_DSN"))
if err != nil {
  log.Fatal(err)
}

sqlTool, err := agentflow.NewSQLToolExecutor(agentflow.SQLToolConfig{
  DB: db,
  AllowedQueries: map[string]string{
    "tickets.by_status": "SELECT `id`, `title`, `status` FROM `tickets` WHERE `status` = ? ORDER BY `created_at` DESC",
  },
  MaxRows: 20,
})
```

### ClickHouse

```go
import (
  "database/sql"

  _ "github.com/ClickHouse/clickhouse-go/v2"
)

db, err := sql.Open("clickhouse", os.Getenv("AGENTFLOW_CLICKHOUSE_DSN"))
if err != nil {
  log.Fatal(err)
}

sqlTool, err := agentflow.NewSQLToolExecutor(agentflow.SQLToolConfig{
  DB: db,
  AllowedQueries: map[string]string{
    "events.hourly": "WITH toStartOfHour(ts) AS bucket SELECT bucket, count() AS total FROM events WHERE ts >= ? GROUP BY bucket ORDER BY bucket DESC LIMIT ?",
  },
  MaxRows: 100,
})
```

The read-only validator understands common PostgreSQL, MySQL, and ClickHouse query shapes including `$1` or `?` placeholders, double-quoted identifiers, backtick identifiers, line comments, block comments, and `WITH ... SELECT` queries. It still rejects multi-statement SQL and mutating keywords such as `INSERT`, `UPDATE`, `DELETE`, `DROP`, `ALTER`, `SELECT ... INTO OUTFILE`, and transaction control statements.

## Tool Input

```json
{
  "query_id": "tickets.by_status",
  "args": ["open"]
}
```

## Tool Output

```json
{
  "query_id": "tickets.by_status",
  "columns": ["id", "title", "status"],
  "rows": [
    {"id": 42, "title": "Retry queue backlog", "status": "open"}
  ],
  "row_count": 1
}
```

For exploratory analytics, point the executor at a read-only replica, keep `MaxRows` low, and enable ad-hoc queries only for trusted scenarios with approval and audit enabled.