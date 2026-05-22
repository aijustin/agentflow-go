package builder

import "github.com/aijustin/agentflow-go/pkg/core"

// CatalogEntry describes a builder stack aligned with an examples/*.yaml file.
type CatalogEntry struct {
	ID    string
	YAML  string
	Build func() core.Scenario
}

// ExampleCatalog returns builder stacks aligned with examples/*.yaml.
func ExampleCatalog() []CatalogEntry {
	return []CatalogEntry{
		{ID: "autonomous-echo", YAML: "autonomous.yaml", Build: func() core.Scenario {
			return MinimalAutonomous("assistant", MinimalScenarioName("autonomous-echo"))
		}},
		{ID: "human-in-loop", YAML: "human_in_loop.yaml", Build: func() core.Scenario {
			return MinimalHumanInLoop("assistant")
		}},
		{ID: "context-governance", YAML: "context_governance.yaml", Build: func() core.Scenario {
			return ContextGovernance("assistant")
		}},
		{ID: "fixed-workflow-review", YAML: "fixed_workflow.yaml", Build: func() core.Scenario {
			return MinimalFixedWorkflowReview("reviewer")
		}},
		{ID: "workflow-enhancements", YAML: "workflow_enhancements.yaml", Build: func() core.Scenario {
			return WorkflowEnhancements()
		}},
		{ID: "code-review-pipeline", YAML: "code_review_pipeline.yaml", Build: func() core.Scenario {
			return CodeReviewPipeline()
		}},
		{ID: "hybrid-research", YAML: "hybrid.yaml", Build: func() core.Scenario {
			return HybridResearch("analyst")
		}},
		{ID: "multi-expert-research", YAML: "multi_expert_research.yaml", Build: func() core.Scenario {
			return MultiExpertResearch()
		}},
		{ID: "adaptive-rag", YAML: "adaptive_rag.yaml", Build: func() core.Scenario {
			return AdaptiveRAG("assistant")
		}},
		{ID: "corrective-rag", YAML: "corrective_rag.yaml", Build: func() core.Scenario {
			return CorrectiveRAG("assistant")
		}},
		{ID: "self-rag", YAML: "self_rag.yaml", Build: func() core.Scenario {
			return SelfRAG("assistant")
		}},
		{ID: "rag-knowledge", YAML: "rag_knowledge.yaml", Build: func() core.Scenario {
			return MinimalRAG("assistant")
		}},
		{ID: "ticket-handling", YAML: "ticket_handling.yaml", Build: func() core.Scenario {
			return MinimalTicketHandling("support")
		}},
		{ID: "tier-memory", YAML: "tier_memory.yaml", Build: func() core.Scenario {
			return TierMemoryAutonomous("assistant")
		}},
		{ID: "http-tool", YAML: "http_tool.yaml", Build: func() core.Scenario {
			return MinimalHTTPTool("assistant")
		}},
		{ID: "sql-tool", YAML: "sql_tool.yaml", Build: func() core.Scenario {
			return MinimalSQLTool("assistant")
		}},
		{ID: "filesystem-tool", YAML: "filesystem_tool.yaml", Build: func() core.Scenario {
			return MinimalFilesystemTool("assistant")
		}},
		{ID: "mcp-tool", YAML: "mcp_tool.yaml", Build: func() core.Scenario {
			return MinimalMCPTool("assistant")
		}},
	}
}
