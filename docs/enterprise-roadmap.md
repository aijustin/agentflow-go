# 企业路线图

这份路线图将 agentflow-go 从可嵌入 Agent 框架推进为企业内部 Agent 平台的基础底座。

## 执行原则

- 先构建运行时基础，再扩展生态。
- 每个里程碑都应独立可测试、可发布。
- 优先使用端口与适配器，而不是 Provider 特定耦合。
- 在增加生产级后端的同时保持本地开发简单。
- 将安全、可观测性和审计能力视为运行时功能，而不是部署后的补丁。

## 里程碑顺序

### M1：生产持久化与恢复

目标：运行、工作流检查点、步骤输出和大型 Blob 在重启与多实例部署后仍然可恢复。

交付物：

- PostgreSQL `RunStateRepository` 适配器。
- S3 兼容 `BlobStore` 适配器，支持 MinIO、AWS S3 path-style endpoint 和私有对象存储。已实现为标准库 SigV4 适配器。
- 基于 Redis 的分布式协调租约适配器。已通过 `SET NX PX` 和原子 Lua 续租/释放实现。
- 用于过期快照、已完成运行和孤儿 Blob 的保留与清理 API。
- 针对跨进程恢复、CAS 冲突处理和 Blob checksum validation 的集成测试。

验收标准：

- 暂停的运行可以由另一个进程或实例恢复。
- 并发恢复尝试保留 CAS 语义，并且只有一个成功。
- 大型步骤输出可以外置到对象存储并被取回。
- 存储失败尽可能返回明确的类型化错误。

### M2：异步运行时与 Worker

目标：通过异步任务提交和可水平扩展 Worker 支持长时间运行的企业工作流。

交付物：

- 任务、队列、Worker 和租约抽象。初始 `pkg/async` 契约已实现。
- 用于测试和本地开发的内存队列。已在 `internal/adapter/queue/inmem` 实现，并通过 `NewInMemoryJobQueue` 暴露。
- 生产基线 PostgreSQL 队列适配器。
- 提交/状态/取消流程的 HTTP API，以及生产健康检查/就绪探针封装。
- 重试、超时、死信和取消语义。

验收标准：

- HTTP 提交返回 `run_id`，不阻塞等待完成。
- 多个 Worker 不会同时执行同一个租约任务。
- 取消会通过运行时上下文传播。
- 失败任务可以重试，并最终标记为死信。

### M3：企业认证、租户与 RBAC

目标：让 runtime API 可以安全地用于公司共享环境。

设计：[security-auth-tenancy.md](security-auth-tenancy.md)

交付物：

- 租户/工作区/项目上下文模型。
- API key 认证中间件。
- OIDC/OAuth2 认证适配器。
- RBAC 策略端口，包含 admin、operator、viewer、approver 和 service principal 角色。
- 租户作用域的运行状态、记忆、Blob、事件和审计记录。

验收标准：

- 租户数据不能跨租户边界加载或恢复。
- 危险工具和 HITL 决策会强制执行角色检查。
- 审批记录包含操作者身份。
- HTTP API 返回一致的 401/403 响应。

### M4：可观测性、审计与治理

目标：让生产行为可诊断、可度量、可治理。

设计：[observability-governance.md](observability-governance.md)

交付物：

- 运行、租户、Agent、工具、步骤和追踪标识的结构化 `slog` 字段。
- OpenTelemetry 追踪。
- 运行时、LLM、工具、工作流和队列行为的 Prometheus 指标。
- 审计 sink 接口和持久审计事件适配器。
- 密钥和敏感数据的脱敏钩子。
- 预算限制、工具副作用、审批门禁和输出检查的策略基线。

验收标准：

- 一个运行可以从 HTTP 请求追踪到工作流步骤、LLM 调用和工具。
- 指标暴露延迟、错误数、token 用量和队列深度。
- 审计日志能回答谁批准、拒绝、修订或调用了高风险工具。
- 敏感值不会出现在日志、事件、快照或调试响应中。

### M5：Provider 能力矩阵、RAG 与知识库

目标：支持企业知识工作流和可预期的模型行为。

交付物：

- 流式输出、工具调用、结构化输出、embedding 和用量统计的 Provider 能力矩阵。初始能力辅助函数已实现。
- Embedding Provider 端口。`llm.Embedder` 已由 OpenAI-compatible 和 mock 适配器实现。
- 向量存储端口和 pgvector 基线适配器。初始 `pkg/knowledge` 和 PostgreSQL pgvector 适配器已实现。
- 文档加载器、分块器、索引器、检索工具和引用/来源追踪。文件和 HTTP 加载器已实现。
- 检索工具。初始语义检索执行器已实现。
- 租户隔离的知识集合。

验收标准：

- 场景可以通过 YAML 将 Agent 绑定到知识集合。
- 检索结果包含来源元数据和引用标识。
- 不支持的 Provider 能力会清晰失败，或通过配置的降级路由处理。
- 租户隔离适用于已索引文档和检索。

### M6：Skill/Tool 生态与部署模板

目标：让 agentflow-go 便于团队扩展、打包、部署和维护。

交付物：

- Skill 包格式、版本管理和兼容性校验。
- 工具包格式、schema 校验和副作用元数据。
- 内部 skill/tool catalog 的注册表接口。
- HTTP、SQL、Git、文件系统、工单、ChatOps 的内置企业工具。初始受约束 HTTP、文件系统读取和 SQL 查询工具执行器已实现。
- Docker Compose 本地企业栈。初始 PostgreSQL+pgvector、Redis、MinIO 和 `agent-http` 栈已在 `deploy/enterprise` 下实现。
- Helm chart 和 Kubernetes manifests。`agent-http` 的初始 Kustomize base 已在 `deploy/kubernetes/base` 下实现。
- 审批、代码评审、工单处理、RAG 问答和多 Agent 工作流示例场景。
- v0 API 稳定性策略和迁移指南。已在 `docs/api-stability.md` 中实现，发布验证指南位于 `docs/release-checklist.md`。

验收标准：

- 团队可以注册新工具和 skill，而不需要修改核心运行时包。
- 包携带版本和兼容性元数据。
- 本地企业栈可以一条命令启动。
- Kubernetes 部署包含运行时、Worker、指标和健康探针。

## 推荐交付顺序

1. M1 持久化与恢复。
2. M2 异步运行时与 Worker。
3. M3 认证、租户与 RBAC。
4. M4 可观测性、审计与治理。
5. M5 Provider 能力矩阵与 RAG。
6. M6 生态与部署模板。

## 当前重点

M1-M4 基础已经以库级切片实现：持久运行状态/Blob/记忆适配器、异步队列/Worker 执行、企业身份/RBAC/审计、结构化 `slog` sink、工具治理、输出脱敏和生产异步 HTTP 路由。M5 现在包括 Provider 能力辅助函数、OpenAI-compatible embeddings、MCP 工具适配器、`pkg/knowledge`、文件/HTTP 文档加载、分块/索引、pgvector 存储、显式检索引用和元数据过滤。M6 已从本地企业 Compose 栈、生产 SQL 迁移、Kustomize base、受约束 HTTP/文件系统读取/SQL 查询工具执行器，以及 v0 API 稳定性和发布检查指南开始。下一步重点是更专用的摄取连接器、Helm chart 打包和更多内置企业工具。