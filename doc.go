// Package agentflow provides a small public facade for embedding the
// scenario-driven agent runtime in other Go projects.
//
// Applications that need low-level extension points can import pkg/core,
// pkg/llm, pkg/memory, and pkg/runstate directly. Applications that only need
// to load a YAML scenario and run it should use this package.
//
// Root package layout:
//   - core runtime: framework.go and the framework_*.go family (run, resume,
//     checkpoints, streaming, workflow/hybrid orchestration)
//   - lifecycle and options: lifecycle.go, builder.go, security.go
//   - HTTP adapters: http_adapters.go (checkpoint, retention, studio,
//     webhook/human-gate, async jobs), observability_http.go (dashboard,
//     studio adapter, audit sinks, event emit helpers)
//   - adapter factories: factories.go (run-state/blob/memory stores, catalog
//     manifests, LLM providers, mock gateway), storage.go (S3 blob store,
//     orphan blob GC), lease.go (run lease, lockers)
//   - scenario wiring: wiring.go (validation, knowledge, MCP), knowledge.go,
//     mcp.go, tools.go, tier_memory.go
//   - retention: retention.go
//   - metadata: meta.go (version, JSON schema, JSON helpers)
package agentflow
