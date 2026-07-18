// Package builder is the **primary** way to construct core.Scenario values for
// agentflow-go. Built scenarios should be validated with
// agentflow.ValidateScenario before creating a Framework.
//
// For the common mock/session/echo autonomous stack:
//
//	scenario := builder.MinimalAutonomous("assistant")
//
// CoreCatalog (default CI / autonomous) and LegacyCatalog (workflow/hybrid/RAG,
// expansion frozen) together form ExampleCatalog(). See docs/orchestration-modes.md.
//
//	scenario := builder.MinimalTicketHandling("support")
//	scenario := builder.ContextGovernance("assistant")
//	scenario := builder.MinimalHumanInLoop("assistant")
//	scenario := builder.MinimalRAG("assistant") // legacy surface
//
// Validate core:  go run ./examples/go/validate -kind builder core
// Validate full:  go run ./examples/go/validate -kind builder full
//
// Full reference: docs/builder-reference.md
//
// For explicit control with named constants instead of string literals:
//
//	scenario := builder.New("my-app").
//	    DefaultMockLLM().
//	    SessionMemory().
//	    EchoTool().
//	    Agent("assistant").DefaultLLM().EchoTool().Autonomous().
//	    Scenario()
package builder
