# 数据生命周期

AgentFlow 提供 run 快照与 Blob 对象的保留/清理 API，用于控制存储成本并避免误删仍在使用的数据。

## Run 快照清理

根门面方法（见 `retention.go`）：

| 方法 | 作用 |
| --- | --- |
| `PurgeRuns(filter)` | 按 `ListFilter` 删除匹配的 run 快照 |
| `PurgeExpired(maxAge)` | 删除终态 run 中 `updated_at` 早于 `now-maxAge` 的记录 |
| `PurgeWithPolicy(policy)` | 组合 `MaxAge`、状态、场景名与条数上限 |

要点：

- 仅当 snapshot JSON 内存在 `updated_at` 时才参与 age 清理；缺少时间戳的旧数据会被跳过。
- 当 HTTP/Worker 上下文携带 principal 时，清理会自动按 `tenant_id` 过滤。
- 所有 RunState 适配器（inmem、file、postgres、redis）在 `Save` 时都会刷新 `updated_at`。

## Blob 孤儿清理

当步骤输出超过 `runtime.step_output_threshold` 时会外置到 `BlobStore`。删除 run 快照不会自动删除 Blob。

```go
removed, err := fw.PurgeOrphanBlobs(ctx)
```

行为：

- 列出当前场景（及 tenant，若有 principal）的全部 run 快照，收集仍被引用的 Blob ID。
- 对实现了 `runstate.BlobAdmin`（`List` + `Delete`）的 BlobStore，删除未被引用的对象。
- 内置 inmem 与 file BlobStore 已实现 `BlobAdmin`；S3 适配器需在上层封装或后续扩展。

建议运维顺序：先 `PurgeExpired` 清理过期 run，再 `PurgeOrphanBlobs` 回收对象存储。

## 记忆命名空间

Session 级记忆在运行时会使用 `{namespace}:{agent_name}` 作为 session 键，详见 [configuration-reference.md](configuration-reference.md#记忆)。
