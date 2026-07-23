// Package adapters collects the convenience constructors for the concrete
// adapters shipped with agentflow: run-state/blob/memory stores, job queues,
// LLM providers, catalog manifests, knowledge and MCP tooling, built-in tool
// executors, tiered memory, and observability sinks/stores. It is a pure
// wiring layer: it never imports the agentflow root facade, so applications
// that only need these constructors can depend on it alone.
package adapters
