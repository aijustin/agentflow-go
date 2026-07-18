package builder

import "github.com/aijustin/agentflow-go/pkg/core"

// CatalogEntry describes a builder stack in ExampleCatalog / CoreCatalog.
type CatalogEntry struct {
	ID    string
	Build func() core.Scenario
}

// CoreCatalog returns the default CI surface: autonomous-first stacks.
// New catalog entries for default validation should belong here unless a
// fixed_workflow/hybrid activation trigger has been met (see docs/orchestration-modes.md).
func CoreCatalog() []CatalogEntry {
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

// LegacyCatalog returns fixed_workflow / hybrid / RAG stacks kept for docs and
// discoverability. Expansion is frozen until activation triggers in
// docs/orchestration-modes.md are met. Not part of default validate-builder.
func LegacyCatalog() []CatalogEntry {
	return []CatalogEntry{
		{ID: "declarative-interrupt", Build: func() core.Scenario {
			return MinimalDeclarativeInterrupt()
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
	}
}

// ExampleCatalog returns CoreCatalog + LegacyCatalog (docs and examples).
func ExampleCatalog() []CatalogEntry {
	core := CoreCatalog()
	legacy := LegacyCatalog()
	out := make([]CatalogEntry, 0, len(core)+len(legacy))
	out = append(out, core...)
	out = append(out, legacy...)
	return out
}
