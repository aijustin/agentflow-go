# 认证、可观测性与治理实施计划

> **给 agentic worker：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans，按任务逐步实施本计划。步骤使用复选框（`- [ ]`）语法跟踪。

**目标：** 增加企业身份、授权、审计、可观测性和治理基础。

**架构：** 先增加身份和策略端口，再将它们接入 HTTP/运行时路径。保持 Debug UI 与生产 API 行为分离。

**技术栈：** Go 1.25.10+、`context`、`log/slog`，Prometheus/OpenTelemetry 依赖需经过显式依赖评审后再引入。

---

## 任务 1：身份与租户上下文

- [x] 增加公共主体、租户、工作区和项目上下文类型。
- [x] 使用未导出的 key 增加上下文辅助函数。
- [x] 增加上下文传播和缺失身份行为测试。

## 任务 2：API Key Middleware

- [x] 增加 API key 认证器端口。
- [x] 增加用于测试和小型部署的静态/内存认证器。
- [x] 增加返回一致 401 响应的 HTTP 中间件。
- [x] 确保密钥永不进入日志。

## 任务 3：授权策略

- [x] 增加动作/资源策略端口。
- [x] 增加基于角色的 allowlist 实现。
- [x] 在配置认证/策略时，强制检查 Debug HTTP 运行提交/读取和 HITL 恢复。
- [x] 强制检查运行时工具调用。
- [x] 在异步 HTTP 取消 API 中强制检查运行取消。
- [x] 增加被拒绝动作测试。

## 任务 4：审计 Sink

- [x] 增加审计事件模型。
- [x] 增加 no-op、内存和文件审计 sink。
- [x] 为运行提交、HITL 决策、工具调用和策略拒绝发出审计事件。
- [x] 为可变审计字段增加防御性复制测试。

## 任务 5：可观测性端口

- [x] 增加结构化运行时操作事件端口。
- [x] 增加 `slog` 实现。
- [ ] 引入 Prometheus 依赖前先定义指标名称和标签。
- [ ] 引入 OpenTelemetry 依赖前先定义 span 名称。

## 任务 6：治理策略

- [x] 增加预算策略接口。
- [x] 增加工具副作用策略接口。
- [x] 增加输出脱敏接口。
- [x] 将策略检查接入工具调用和结果持久化。

## 验证

- [x] `gofmt -w .`
- [x] `CGO_ENABLED=0 go test -ldflags="-w" ./...`
- [x] `CGO_ENABLED=0 go vet ./...`
- [x] `make build`