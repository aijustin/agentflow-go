# SQL 工具执行器

`agentflow.NewSQLToolExecutor` 将受约束的只读 SQL 查询暴露为普通的 `core.ToolExecutor`。它适合运维看板、工单元数据、报表表，以及 Agent 需要数据库上下文的其他低风险查询流程。

## 安全模型

该执行器默认拒绝访问：

- 宿主应用必须提供 `*sql.DB`。
- 优先使用命名 `AllowedQueries`，并在构造时校验。
- 除非显式启用 `AllowAdHocQuery`，否则禁用临时 SQL。
- 只接受 `SELECT` 查询。
- 拒绝语句终止符，避免多语句执行。
- 在字符串字面量和注释之外，拒绝明显的变更型 SQL 关键字，例如 `INSERT`、`UPDATE`、`DELETE`、`DROP`、`ALTER`。
- 查询执行使用带超时的 `QueryContext`，默认超时为 5 秒。
- 返回行数有上限，默认限制为 100 行。
- 结果以结构化 JSON 返回，包含列、行、行数和截断状态。

请使用只读权限和最小表访问权限的数据库角色。该执行器是工具边界的护栏，不是数据库授权的替代品。

## 装配

SQL 工具与具体数据库驱动解耦。框架接受 `*sql.DB`，不导入具体驱动，因此宿主应用负责选择驱动和 DSN。查询占位符不会被重写，请使用所选驱动期望的占位符语法。

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

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithToolExecutor("sql.query", sqlTool),
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

只读校验器理解常见 PostgreSQL、MySQL 和 ClickHouse 查询形态，包括 `$1` 或 `?` 占位符、双引号标识符、反引号标识符、行注释、块注释，以及 `WITH ... SELECT` 查询。它仍会拒绝多语句 SQL 和变更型关键字，例如 `INSERT`、`UPDATE`、`DELETE`、`DROP`、`ALTER`、`SELECT ... INTO OUTFILE` 和事务控制语句。

## 工具输入

```json
{
  "query_id": "tickets.by_status",
  "args": ["open"]
}
```

## 工具输出

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

对于探索式分析，请将执行器指向只读副本，保持 `MaxRows` 较低，并且只在可信场景中启用临时查询，同时开启审批和审计。