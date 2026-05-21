# PostgreSQL 运行状态适配器

PostgreSQL 适配器会在既有 `runstate.Repository` 契约背后持久化 `runstate.RunSnapshot`。它面向需要重启安全 HITL 恢复和多实例 CAS 保护的生产部署。

## 驱动策略

agentflow-go 不直接导入 PostgreSQL 驱动。应用注册自己偏好的 `database/sql` 驱动，并将初始化好的 `*sql.DB` 传入根构造函数。

可选驱动包括 `github.com/jackc/pgx/v5/stdlib` 和 `github.com/lib/pq`。驱动依赖应放在拥有部署和连接池策略的应用中。

```go
package main

import (
    "database/sql"
    "log"
    "time"

    agentflow "github.com/aijustin/agentflow-go"
    _ "github.com/jackc/pgx/v5/stdlib"
)

func openRunState(dsn string) agentflow.Option {
    db, err := sql.Open("pgx", dsn)
    if err != nil {
        log.Fatal(err)
    }
    db.SetMaxOpenConns(25)
    db.SetMaxIdleConns(10)
    db.SetConnMaxLifetime(5 * time.Minute)

    runs, err := agentflow.NewPostgresRunStateRepository(db)
    if err != nil {
        log.Fatal(err)
    }
    return agentflow.WithRunStateRepository(runs)
}
```

## 表契约

默认表名是 `agentflow_run_snapshots`。也可以通过可选第二个参数传入带 schema 的表名，例如 `agentflow.run_snapshots`。

适配器期望这些列：

| 列 | 作用 |
| --- | --- |
| `run_id` | 唯一运行标识，用于加载、删除和插入冲突检测。 |
| `version` | CAS 版本。更新会同时匹配 `run_id` 和 `version`。 |
| `scenario_name` | 场景名称，便于运维查询。 |
| `status` | 当前运行状态。 |
| `current_node_id` | 工作流恢复位置。 |
| `snapshot_json` | 完整序列化的 `RunSnapshot` JSON 载荷，包含 `created_at`、`updated_at` 和 `tenant_id`。 |
| `updated_at` | 适配器保存时更新的数据库列时间戳。 |

`PurgeExpired` 依据 `snapshot_json` 内的 `updated_at` 判断 age；旧数据若缺少该字段会被跳过而不是误删。

迁移 SQL 应放在拥有生产 schema 评审的部署包中。适配器有意不自动创建或修改数据库 schema。

## CAS 语义

- `Save(ctx, snapshot, 0)` 插入新行；如果运行已存在，返回 `runstate.ErrStaleSnapshot`。
- `Save(ctx, snapshot, expectedVersion)` 只有在已存版本等于 `expectedVersion` 时才更新。
- 成功保存时，如果传入的版本没有大于期望版本，会递增 `snapshot.Version`。
- `Load` 对缺失行返回 `runstate.ErrNotFound`。
- 从框架视角看，`Delete` 是幂等操作。

## 运维注意事项

- 在请求和 Worker 路径上使用上下文 deadline。
- 构造仓库前，在应用中配置连接池。
- 表名应保持静态并经过评审。构造函数会校验表标识符并拒绝不安全名称。
- 大型工作流输出应存入 `BlobStore`；运行状态应只保存 `BlobRef` 元数据，而不是大型原始载荷。