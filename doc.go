// Package agentflow provides a small public facade for embedding the
// scenario-driven agent runtime in other Go projects.
//
// Applications that need low-level extension points can import pkg/core,
// pkg/llm, pkg/memory, and pkg/runstate directly. Applications that only need
// to load a YAML scenario and run it should use this package.
//
// Root package layout:
//   - core runtime: framework.go and the framework_*.go family (run, resume,
//     checkpoints, streaming, workflow/hybrid orchestration, async job
//     handler, event emit helpers)
//   - lifecycle and options: lifecycle.go, builder.go, security.go
//   - wiring validation: wiring.go (ValidateWiring and the New-time wiring
//     checks; scenario knowledge/MCP wiring constructors live in pkg/httpx
//     because they return []agentflow.Option)
//   - retention: retention.go (purge policies, orphan blob GC)
//   - run lease: lease.go
//   - metadata: meta.go (version, JSON schema, JSON helpers)
//
// Convenience constructors that used to live here moved in v0.3:
//   - pkg/adapters: concrete adapter constructors (run-state/blob/memory
//     stores, job queues, LLM providers, catalog manifests, knowledge, MCP,
//     tool executors, tiered memory, observability sinks/stores). It never
//     imports this root package.
//   - pkg/httpx: HTTP adapter constructors (checkpoint, retention, studio,
//     webhook/human-gate, async jobs, production composition, observability
//     dashboard) and the knowledge/MCP wiring options.
//   - pkg/testutil: the mock LLM gateway and test wiring helpers.
package agentflow
