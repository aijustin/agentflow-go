# HTTP Tool Executor

`agentflow.NewHTTPToolExecutor` exposes a constrained HTTP client as a normal `core.ToolExecutor`. It is intended for read-only integrations such as internal status APIs, documentation gateways, and service metadata endpoints.

## Safety Model

The executor is deny-by-default:

- At least one allowed host is required.
- Only `GET` and `HEAD` are allowed unless `AllowedMethods` is explicitly configured.
- Only `http` and `https` URLs are accepted.
- Responses are size-limited. The default limit is 1 MiB.
- The result is structured JSON with status code, headers, and body.

This keeps the runtime governance path intact: agent tool allowlists, RBAC, approval policy, side-effect policy, rate caps, audit, and output redaction still apply before and after tool execution.

## Wiring

```go
httpTool, err := agentflow.NewHTTPToolExecutor(agentflow.HTTPToolConfig{
  AllowedHosts:     []string{"https://status.example.internal"},
  AllowedMethods:   []string{"GET", "HEAD"},
  DefaultHeaders:   map[string]string{"Accept": "application/json"},
  MaxResponseBytes: 1 << 20,
})
if err != nil {
  log.Fatal(err)
}

fw, err := agentflow.NewFromFile(
  "scenario.yaml",
  agentflow.WithToolExecutor("http.status", httpTool),
)
```

## Tool Input

```json
{
  "method": "GET",
  "url": "https://status.example.internal/health",
  "headers": {"X-Request-Source": "agentflow"}
}
```

## Tool Output

```json
{
  "status_code": 200,
  "headers": {"Content-Type": "application/json"},
  "body": "{\"ok\":true}"
}
```

For mutating APIs, prefer a purpose-built tool with domain-specific validation over broad `POST`/`PUT` access.