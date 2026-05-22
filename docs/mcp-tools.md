# MCP 工具集成

AgentFlow 通过适配器集成 MCP server，而不是把 MCP 烘焙进运行时核心：

```text
MCP server -> MCP client adapter -> core.ToolExecutor -> AgentFlow runtime
```

这样可以保持场景和治理策略不变。MCP 支撑的工具仍然会经过 Agent 工具 allowlist、审批策略、RBAC 策略、审计 sink、速率限制、副作用检查和输出脱敏。

## HTTP JSON-RPC 客户端

当 MCP server 通过 HTTP JSON-RPC 端点暴露时，使用 `NewMCPHTTPClient`：

```go
mcpClient, err := agentflow.NewMCPHTTPClient("http://127.0.0.1:3333/mcp", nil)
if err != nil {
  log.Fatal(err)
}
```

客户端支持 `tools/list` 和 `tools/call`。它有意只依赖标准库 `net/http` 客户端，因此应用可以注入自定义 TLS、代理、重试或认证 transport。

## Stdio 客户端

当 MCP server 以本地子进程方式运行时，使用 `NewMCPStdioClient`：

```go
mcpClient, err := agentflow.NewMCPStdioClient(ctx, agentflow.MCPStdioClientConfig{
  Command: "node",
  Args:    []string{"./mcp-server.js"},
  Env:     []string{"MCP_MODE=stdio"},
  Dir:     "./tools/docs-search",
})
if err != nil {
  log.Fatal(err)
}
defer mcpClient.Close()
```

Stdio 客户端同样支持 `tools/list` 和 `tools/call`，并通过 `exec.CommandContext` 直接传递命令和参数，不经过 shell 拼接。应用应只运行可信的 MCP server，并将命令、工作目录和环境变量来自受控配置或部署系统。

## 工具执行器适配器

将一个 MCP server 工具包装为普通 AgentFlow `core.ToolExecutor`：

```go
searchTool, err := agentflow.NewMCPToolExecutor(mcpClient, "search")
if err != nil {
  log.Fatal(err)
}

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithToolExecutor("docs.search", searchTool),
)
```

场景中声明工具元数据和治理策略：

```yaml
tools:
  docs.search:
    type: mcp.tool
    description: 搜索公司文档 MCP server。
    side_effect: read
    approval: never
```

`CallToolResult.isError` 会映射到 `core.ToolResult.Error`，完整 MCP result 会保留在 JSON 输出中。这样 LLM 既能获得足够细节继续工具循环，又能保留清晰的运行时错误信号。

## 生产注意事项

- 保持 MCP server 具备租户感知能力，或按租户边界注册独立执行器。
- 使用 AgentFlow RBAC 和治理策略做粗粒度控制，再在 MCP server 内部执行资源级授权。
- MCP server 内优先使用短工具名，在 AgentFlow 场景中使用稳定的场景级名称。场景名称是策略表面，MCP 名称是适配器目标。
- 对 stdio server 使用最小权限运行用户和固定工作目录；不要把用户输入直接映射为命令或参数。
- 除非远程 MCP server 严格只读且可信，否则应将其视为外部副作用。