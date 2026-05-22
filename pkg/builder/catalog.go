package builder

import "github.com/aijustin/agentflow-go/pkg/core"

// CatalogEntry describes a builder stack in ExampleCatalog.
type CatalogEntry struct {
	ID    string
	Build func() core.Scenario
}

// ExampleCatalog returns the standard builder stacks used in docs and examples.
func ExampleCatalog() []CatalogEntry {
	return []CatalogEntry{
		{ID: "autonomous-echo", Build: func() core.Scenario {
			return MinimalAutonomous("assistant", MinimalScenarioName("autonomous-echo"))
		}},
		{ID: "human-in-loop", Build: func() core.Scenario {
			return MinimalHumanInLoop("assistant")
		}},
		{ID: "context-governance", Build: func() core.Scenario {
			return ContextGovernance("assistant")
		}},
		{ID: "fixed-workflow-review", Build: func() core.Scenario {
			return MinimalFixedWorkflowReview("reviewer")
		}},
		{ID: "workflow-enhancements", Build: func() core.Scenario {
			return WorkflowEnhancements()
		}},
		{ID: "code-review-pipeline", Build: func() core.Scenario {
			return CodeReviewPipeline()
		}},
		{ID: "hybrid-research", Build: func() core.Scenario {
			return HybridResearch("analyst")
		}},
		{ID: "multi-expert-research", Build: func() core.Scenario {
			return MultiExpertResearch()
		}},
		{ID: "adaptive-rag", Build: func() core.Scenario {
			return AdaptiveRAG("assistant")
		}},
		{ID: "corrective-rag", Build: func() core.Scenario {
			return CorrectiveRAG("assistant")
		}},
		{ID: "self-rag", Build: func() core.Scenario {
			return SelfRAG("assistant")
		}},
		{ID: "rag-knowledge", Build: func() core.Scenario {
			return MinimalRAG("assistant")
		}},
		{ID: "ticket-handling", Build: func() core.Scenario {
			return MinimalTicketHandling("support")
		}},
		{ID: "tier-memory", Build: func() core.Scenario {
			return TierMemoryAutonomous("assistant")
		}},
		{ID: "http-tool", Build: func() core.Scenario {
			return MinimalHTTPTool("assistant")
		}},
		{ID: "sql-tool", Build: func() core.Scenario {
			return MinimalSQLTool("assistant")
		}},
		{ID: "filesystem-tool", Build: func() core.Scenario {
			return MinimalFilesystemTool("assistant")
		}},
		{ID: "mcp-tool", Build: func() core.Scenario {
			return MinimalMCPTool("assistant")
		}},
	}
}
