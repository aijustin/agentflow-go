# 企业部署模板

仓库在 [deploy/enterprise](../deploy/enterprise) 下提供本地企业级 Compose 栈。它用于开发、集成测试，也可以作为生产部署的具体参考。

## 包含的服务

- PostgreSQL，包含 pgvector，以及用于运行状态、异步任务和知识库向量的初始化 SQL。
- Redis，用于分布式协调租约。
- MinIO，用于 S3 兼容 Blob 存储。
- `agent-http`，用于调试控制台和 HTTP HITL 桥接。

## 生产迁移

版本化 PostgreSQL 迁移位于 [deploy/migrations/postgres](../deploy/migrations/postgres)。本地 Compose 栈在数据库初始化时复用同一份 `0001_agentflow_core.up.sql`。

应用团队可以将这些 SQL 文件接入自己选择的迁移执行器。如果 embedding 模型不是 1536 维向量，应在应用自有迁移中调整 pgvector 维度。

## 快速开始

```sh
cd deploy/enterprise
cp .env.example .env
docker compose up --build
```

调试控制台默认监听 `http://localhost:18080`。API key、token secret、数据库密码和 MinIO 凭证通过 `.env` 控制。

## 与 AgentFlow 构造函数的映射

- PostgreSQL 运行快照：`agentflow.NewPostgresRunStateRepository(db)`
- PostgreSQL 异步任务：`agentflow.NewPostgresJobQueue(db)`
- PostgreSQL pgvector 知识库：`agentflow.NewPostgresVectorStore(...)`
- Redis 租约：`agentflow.NewRedisLocker(...)`
- S3 兼容 Blob：`agentflow.NewS3BlobStore(...)`

框架仍然坚持库优先。生产服务应该自行拥有驱动导入、连接池、迁移策略、密钥和 Worker 进程模型。

## 下一层部署能力

Compose 栈是本地基线。[deploy/kubernetes/base](../deploy/kubernetes/base) 中提供了 `agent-http` 的最小 Kustomize base。Worker 具有应用特异性，因为宿主服务拥有场景装配、数据库驱动、队列选择和工具执行器，所以仓库提供 `worker-deployment.example.yaml` 作为模板，而不是提供一个可直接运行的通用 Worker。