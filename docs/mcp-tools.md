# MCP Tool Integration

AgentFlow integrates MCP servers through adapters instead of baking MCP into the runtime core:

```text
MCP server -> MCP client adapter -> core.ToolExecutor -> AgentFlow runtime
```

This keeps scenarios and governance policies unchanged. MCP-backed tools still pass through agent tool allowlists, approval policy, RBAC policy, audit sinks, rate caps, side-effect checks, and output redaction.

## HTTP JSON-RPC Client

Use `NewMCPHTTPClient` for MCP servers exposed over an HTTP JSON-RPC endpoint:

```go
mcpClient, err := agentflow.NewMCPHTTPClient("http://127.0.0.1:3333/mcp", nil)
if err != nil {
  log.Fatal(err)
}
```

The client supports `tools/list` and `tools/call`. It intentionally depends only on the standard library `net/http` client so applications can inject custom TLS, proxy, retry, or authentication transports.

## Tool Executor Adapter

Wrap one MCP server tool as a normal AgentFlow `core.ToolExecutor`:

```go
searchTool, err := agentflow.NewMCPToolExecutor(mcpClient, "search")
if err != nil {
  log.Fatal(err)
}

fw, err := agentflow.NewFromFile(
  "scenario.yaml",
  agentflow.WithToolExecutor("docs.search", searchTool),
)
```

The scenario declares the tool metadata and governance policy:

```yaml
tools:
  docs.search:
    type: mcp.tool
    description: Search the company documentation MCP server.
    side_effect: read
    approval: never
```

`CallToolResult.isError` is mapped to `core.ToolResult.Error`, while the complete MCP result is preserved in the JSON output. This gives the LLM enough detail to continue the tool loop while preserving a clear runtime error signal.

## Production Notes

- Keep MCP servers tenant-aware or register separate executors per tenant boundary.
- Use AgentFlow RBAC and governance policies for coarse-grained controls, then enforce resource-level authorization inside the MCP server.
- Prefer short tool names in the MCP server and stable scenario-level names in AgentFlow. The scenario name is the policy surface; the MCP name is the adapter target.
- Treat remote MCP servers as external side effects unless they are strictly read-only and trusted.