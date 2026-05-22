// Package builder is the **primary** way to construct core.Scenario values for
// agentflow-go. Built scenarios should be validated with
// agentflow.ValidateScenario before creating a Framework.
//
// YAML loading (LoadScenarioFile / NewFromFile) is deprecated; see
// docs/product-direction.md.
//
// For the common mock/session/echo autonomous stack:
//
//	scenario := builder.MinimalAutonomous("assistant")
//
// Example stacks aligned with ExampleCatalog() IDs:
//
//	scenario := builder.MinimalTicketHandling("support")
//	scenario := builder.MinimalRAG("assistant")
//	scenario := builder.TierMemoryAutonomous("assistant")
//	scenario := builder.AdaptiveRAG("assistant")
//	scenario := builder.CorrectiveRAG("assistant")
//	scenario := builder.SelfRAG("assistant")
//	scenario := builder.HybridResearch("analyst")
//	scenario := builder.MinimalHumanInLoop("assistant")
//	scenario := builder.MultiExpertResearch()
//	scenario := builder.CodeReviewPipeline()
//	scenario := builder.WorkflowEnhancements()
//	scenario := builder.ContextGovernance("assistant")
//	scenario := builder.MinimalFixedWorkflowReview("reviewer")
//	scenario := builder.MinimalHTTPTool("assistant")
//	scenario := builder.MinimalSQLTool("assistant")
//	scenario := builder.MinimalFilesystemTool("assistant")
//	scenario := builder.MinimalMCPTool("assistant")
//
// Validate all stacks: go run ./examples/go/validate -kind builder all
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
