# MCP 工具集成

AgentFlow 通过适配器集成 MCP server，而不是把 MCP 烘焙进运行时核心：

```text
MCP server -> MCP client adapter -> core.ToolExecutor -> AgentFlow runtime
```

这样可以保持场景和治理策略不变。MCP 支撑的工具仍然会经过 Agent 工具 allowlist、审批策略、RBAC 策略、审计 sink、速率限制、副作用检查和输出脱敏。

## HTTP JSON-RPC 客户端

当 MCP server 通过 HTTP JSON-RPC 端点暴露时，使用 `NewMCPHTTPClient`：

```go
mcpClient, err := adapters.NewMCPHTTPClient("http://127.0.0.1:3333/mcp", nil)
if err != nil {
  log.Fatal(err)
}
```

客户端支持 `tools/list` 和 `tools/call`。它有意只依赖标准库 `net/http` 客户端，因此应用可以注入自定义 TLS、代理、重试或认证 transport。

### 显式协议模式

现有构造器默认使用 legacy `2025-11-25`（`initialize` + session）。对
modern `2026-07-28` 无状态服务必须显式选择模式：

```go
mcpClient, err := adapters.NewMCPHTTPClientWithOptions(endpoint, httpClient, mcp.ClientOptions{
  Mode: mcp.ProtocolModeModern,
})
```

Stdio 使用 `MCPStdioClientConfig.Options` 设置同一选项。客户端不会探测、
自动降级或在协议版本间重试；配置与服务端不匹配时直接返回错误。

### Multi Round-Trip Requests 与 elicitation

`2026-07-28` 把服务端「反向发起请求」的旧流程换成了 **MRTR**：无状态服务不再
回连客户端并挂住连接，而是用 `InputRequiredResult` 代替结果返回——里面带着它
需要被回答的 `inputRequests`，以及一段不透明的 `requestState`。客户端收集答案
后，把**原始调用**连同 `inputResponses` 和原样回传的 `requestState` 重发一次。
因为所有状态都在载荷里，重试可以落到任意一个服务实例上。

客户端侧已实现该循环。宿主提供一个 `mcp.Elicitor` 来回答服务端的提问：

```go
opts, err := httpx.MCPWiringOptions(ctx, scenario, httpx.MCPRegistry{
  Elicitor: mcp.ElicitorFunc(func(ctx context.Context, req mcp.ElicitRequest) (mcp.ElicitResult, error) {
    // req.Mode 为 form 或 url；url 模式是规范要求服务端用于凭据的方式。
    // 宿主可以渲染表单、接到 HITL human gate 上、或直接拒绝。
    return mcp.ElicitResult{Action: mcp.ElicitAccept, Content: answer}, nil
  }),
})
```

几条契约值得注意：

- **不配 `Elicitor` 就不声明 `elicitation` 能力**，规范要求服务端只能提出客户端
  已声明支持的请求。能力位由是否存在 `Elicitor` 推导，不接受调用方单独设置，
  避免「声明了却没人应答」把服务端挂死。
- **未配置时收到提问直接报错**，不猜测、不静默跳过——服务端正在等这个答案。
- **`sampling/createMessage` 与 `roots/list` 一律拒绝**。二者在引入 MRTR 的同一
  版本里已被废弃（分别改为宿主直连 LLM provider、改用工具参数），本客户端不
  声明、也不实现。
- **轮次有上限**（`MaxInputRounds`，默认 8），避免服务端反复追问把调用方挂住。
- MRTR 仅在 `ProtocolModeModern` 下驱动；legacy 模式收到 `input_required` 会明确
  报错而不是发送服务端读不懂的字段。

`ElicitResult.Action` 为 `accept` / `decline` / `cancel`。`decline` 是**合法答案**
而非错误，如何处理由服务端决定。

## Stdio 客户端

当 MCP server 以本地子进程方式运行时，使用 `NewMCPStdioClient`：

```go
mcpClient, err := adapters.NewMCPStdioClient(ctx, adapters.MCPStdioClientConfig{
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

通过 `httpx.MCPWiringOptions` / `WireMCPTools` 从场景自动创建的客户端会注册到
`Framework.Close()`：legacy HTTP session 会先终止，stdio 子进程会关闭。通过
`MCPRegistry.Clients` 注入的客户端仍由调用方管理生命周期。

## 工具执行器适配器

将一个 MCP server 工具包装为普通 AgentFlow `core.ToolExecutor`：

```go
searchTool, err := adapters.NewMCPToolExecutor(mcpClient, "search")
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