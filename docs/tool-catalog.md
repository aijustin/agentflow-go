# Deferred Tool Catalog

The `pkg/toolcatalog` package provides keyword search and schema loading for
large tool surfaces. MCP clients default to legacy protocol `2025-11-25`;
select modern stateless `2026-07-28` explicitly with
`mcp.ClientOptions{Mode: mcp.ProtocolModeModern}`. Clients never auto-fallback
between protocol eras.

When attached to a `Framework` via `WithToolCatalog`,
the runtime advertises only:

- `search_tools` — keyword search over catalog entries
- `load_tool_schemas` — load full schemas for named tools
- `compact_context` — signal sub-task completion so the runtime drops masked
  tool observations before the next model turn (also available when
  `ObservationMaskAfterTurns` is configured without a catalog)
- pinned catalog tools and non-MCP builtin tools declared on the agent

All other deferred tools stay hidden until the model calls
`load_tool_schemas`, at which point their schemas are injected for subsequent
turns in the same run. Executors are still resolved through the normal
`WithToolExecutor` / `WithToolResolver` path once a tool is invoked.
Lazy executors use a principal-scoped LRU cache capped at 1024 entries by
default; set `WithToolResolverCacheLimit` to tune the bound or `0` to disable
caching.

Append `toolcatalog.SelfCompactRubric()` to agent instructions when
`compact_context` is enabled so the model knows when to compact.

## Example

```go
catalog := toolcatalog.NewSnapshot("2026-07-25", time.Hour, []toolcatalog.Entry{
    {Name: "docs.search", Description: "Search docs", Pin: true},
    {Name: "sql.query", Description: "Run SQL"},
})

fw, err := agentflow.New(scenario,
    agentflow.WithToolCatalog(catalog),
    agentflow.WithToolExecutor("docs.search", docsTool),
    agentflow.WithToolResolver(sqlResolver),
    agentflow.WithToolResolverCacheLimit(256),
)
```

Use `WithDeferredTools(false)` to attach a catalog for search/load helpers
without deferring non-pinned tool schemas.
