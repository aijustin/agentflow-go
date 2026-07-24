# HTTP 工具执行器

`adapters.NewHTTPToolExecutor` 将受约束的 HTTP 客户端暴露为普通的 `core.ToolExecutor`。它适合只读集成，例如内部状态 API、文档网关和服务元数据端点。

## 安全模型

该执行器默认拒绝访问：

- 必须至少配置一个允许访问的主机。
- 除非显式配置 `AllowedMethods`，否则只允许 `GET` 和 `HEAD`。
- 只接受 `http` 和 `https` URL。
- 响应大小受限，默认限制为 1 MiB。
- 结果以结构化 JSON 返回，包含状态码、响应头和响应体。

这可以保持运行时治理路径不变：Agent 工具 allowlist、RBAC、审批策略、副作用策略、速率限制、审计和输出脱敏，都会在工具执行前后继续生效。

## 装配

```go
httpTool, err := adapters.NewHTTPToolExecutor(adapters.HTTPToolConfig{
  AllowedHosts:     []string{"https://status.example.internal"},
  AllowedMethods:   []string{"GET", "HEAD"},
  DefaultHeaders:   map[string]string{"Accept": "application/json"},
  MaxResponseBytes: 1 << 20,
})
if err != nil {
  log.Fatal(err)
}

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithToolExecutor("http.status", httpTool),
)
```

## 工具输入

```json
{
  "method": "GET",
  "url": "https://status.example.internal/health",
  "headers": {"X-Request-Source": "agentflow"}
}
```

## 工具输出

```json
{
  "status_code": 200,
  "headers": {"Content-Type": "application/json"},
  "body": "{\"ok\":true}"
}
```

对于会修改状态的 API，优先编写带领域校验的专用工具，而不是开放宽泛的 `POST`/`PUT` 能力。
## 幂等

运行时注入的幂等键非空时，执行器会自动把键放进 `X-Idempotency-Key` 请求头（框架值权威，会覆盖默认头与工具输入头中的同名字段）。恢复重放同一逻辑执行时键不变，上游 API 可按该头去重副作用；键的组成与稳定性契约见 [tool-idempotency.md](tool-idempotency.md)。
