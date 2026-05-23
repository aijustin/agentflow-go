# 企业持久化实施计划

> **历史文档**：本计划为 2026-05 实施记录，勾选框反映当时进度，不再作为当前 backlog。产品方向见 [product-direction.md](../../product-direction.md)。

> **给 agentic worker：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans，按任务逐步实施本计划。步骤使用复选框（`- [ ]`）语法跟踪。

**目标：** 为运行状态和大型输出增加生产级持久化基础。

**架构：** 保持既有 `runstate.Repository` 和 `runstate.BlobStore` 契约稳定。通过构造函数暴露生产适配器，集成测试使用 build tag 控制，并且不引入 ORM 依赖。

**技术栈：** Go 1.25.10+、`database/sql`、PostgreSQL 兼容 SQL、后续切片中的 S3 兼容对象存储、带 build tag 的集成测试。

---

## 文件结构

- 创建：`internal/adapter/runstate/postgres/repository.go`，用于 PostgreSQL 兼容运行状态持久化。
- 创建：`internal/adapter/runstate/postgres/repository_test.go`，使用本地测试驱动或集成 build tag 测试 repository 契约。
- 修改：`framework.go`，在适配器可以作为公共扩展点时暴露根构造函数。
- 修改：`README.md` 和 `README.en.md`，增加生产持久化示例。
- 修改：`docs/enterprise-roadmap.md`，随着里程碑完成更新。

## 任务 1：PostgreSQL RunStateRepository

- [x] 编写失败测试：`Save` 在 expected version 为 0 时插入 version 1。
- [x] 编写失败测试：expected version 不匹配时，`Save` 返回 `runstate.ErrStaleSnapshot`。
- [x] 编写失败测试：无记录时，`Load` 返回 `runstate.ErrNotFound`。
- [x] 使用调用方提供的 `*sql.DB` 实现 `Repository`。
- [x] 将完整快照存为 JSON，并暴露关键列以便查询和清理。
- [x] 只使用参数化 SQL。
- [x] 使用条件更新保留 CAS 语义。
- [x] 运行 `CGO_ENABLED=0 go test -ldflags="-w" ./...`。

## 任务 2：公共构造函数

- [x] 在根门面增加 `NewPostgresRunStateRepository(db *sql.DB, tableName ...string)`。
- [x] 记录调用方必须在应用代码中注册 PostgreSQL driver。
- [x] 增加无效公共输入的构造函数测试，以及适配器的 repository 契约测试。
- [x] 运行 `CGO_ENABLED=0 go test -ldflags="-w" ./...`。

## 任务 3：S3 兼容 BlobStore

- [x] 增加 SDK 前先决定依赖策略。
- [x] 增加支持 MinIO/私有 S3 endpoint override 的适配器。
- [x] 读取时校验校验和和大小。
- [x] 使用 HTTP S3-compatible fake 增加单元测试。
- [x] 记录必要凭证和 endpoint 配置。

## 任务 4：Redis 协调适配器

- [x] 决定第一批中 Redis 用于运行状态、锁还是队列。
- [x] 如果 Redis 只用于协同，则增加小型锁/租约接口。
- [x] 通过 Redis Lua script 增加原子续租/释放行为。
- [x] 使用 fake Redis server 增加单元测试。

## 任务 5：保留与清理

- [ ] 为超过截止时间的 completed/failed/cancelled 运行增加清理 API。
- [ ] 增加孤儿 Blob 清理策略。
- [ ] 记录运维保留策略。

## 验证

- [ ] `gofmt -w .`
- [ ] `CGO_ENABLED=0 go test -ldflags="-w" ./...`
- [ ] `CGO_ENABLED=0 go vet ./...`
- [ ] `make build`
- [ ] 在可用时使用本地 PostgreSQL/Redis/MinIO 运行集成测试。

## 注意事项

- 不引入 ORM。
- 根模块不要求特定 PostgreSQL 驱动；接受 `*sql.DB`，让应用导入自己偏好的驱动。
- 在后续生产部署包中，将迁移保持为经过评审的 SQL 工件。