package builder

import "github.com/aijustin/agentflow-go/pkg/core"

// MinimalTicketHandling builds the ticket-handling stack from examples/ticket_handling.yaml.
func MinimalTicketHandling(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	cfg.scenarioName = "ticket-handling"
	cfg.instructions = "You are a support agent. Review ticket context, draft a helpful reply, and wait for approval before finalizing sensitive responses."
	cfg.tool = minimalToolTicket
	for _, opt := range opts {
		opt(&cfg)
	}
	b := New(cfg.scenarioName).
		Description("Customer support ticket triage with knowledge retrieval and human approval.").
		StandardTicketStack()
	ab := b.Agent(agentName).
		PrimaryLLM().
		SessionMemory().
		TicketTool().
		Instructions(cfg.instructions)
	return ab.Autonomous().
		BeforeFinalAnswerHITL().
		TicketCreatedTrigger(agentName).
		Scenario()
}

// MinimalRAG builds the rag-knowledge stack from examples/rag_knowledge.yaml.
func MinimalRAG(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	cfg.scenarioName = "rag-knowledge-example"
	cfg.instructions = "Use knowledge.retrieve before answering questions about indexed internal documents."
	cfg.tool = minimalToolKnowledge
	for _, opt := range opts {
		opt(&cfg)
	}
	b := New(cfg.scenarioName).RAGStack()
	ab := b.Agent(agentName).
		ChatLLM().
		KnowledgeRetrieveTool().
		Instructions(cfg.instructions)
	return ab.Autonomous().Scenario()
}

// TierMemoryAutonomous builds the tier-memory demo from examples/tier_memory.yaml.
func TierMemoryAutonomous(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	cfg.scenarioName = "tier-memory-demo"
	cfg.instructions = "Answer the user clearly."
	cfg.tool = minimalToolEcho
	for _, opt := range opts {
		opt(&cfg)
	}
	b := New(cfg.scenarioName).
		DefaultMockLLM().
		TierSessionMemory("tier-demo")
	b.EchoTool()
	ab := b.Agent(agentName).
		DefaultLLM().
		Memory(NameSessionMemory).
		EchoTool().
		Instructions(cfg.instructions)
	return ab.Autonomous().Scenario()
}

// AdaptiveRAG builds the adaptive RAG workflow from examples/adaptive_rag.yaml.
func AdaptiveRAG(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	cfg.scenarioName = "adaptive-rag"
	cfg.instructions = "Answer directly when route is direct; otherwise use retrieved evidence."
	cfg.tool = minimalToolKnowledge
	for _, opt := range opts {
		opt(&cfg)
	}
	b := New(cfg.scenarioName).
		Description("Adaptive RAG workflow that routes factual questions to retrieval and direct questions to the agent.").
		RAGStack()
	b.DocsKnowledgeCollection(CollectionTenantScoped(true))
	ab := b.Agent(agentName).
		ChatLLM().
		KnowledgeRetrieveTool().
		Instructions(cfg.instructions)
	wf := AdaptiveRAGWorkflow(NameKnowledgeCollectionDocs, agentName)
	return ab.FixedWorkflow(wf).Scenario()
}

// CorrectiveRAG builds the corrective-rag workflow from examples/corrective_rag.yaml.
func CorrectiveRAG(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	cfg.scenarioName = "corrective-rag"
	cfg.instructions = "Answer using retrieved evidence. If grade.relevant is false, call knowledge.retrieve again with grade.rewrite_query."
	cfg.tool = minimalToolKnowledge
	for _, opt := range opts {
		opt(&cfg)
	}
	b := New(cfg.scenarioName).
		Description("Corrective RAG workflow that grades retrieval quality and rewrites the query when evidence is weak.").
		RAGStack()
	ab := b.Agent(agentName).
		ChatLLM().
		KnowledgeRetrieveTool().
		Instructions(cfg.instructions)
	wf := CorrectiveRAGWorkflow(NameKnowledgeCollectionDocs, agentName)
	return ab.FixedWorkflow(wf).Scenario()
}

// SelfRAG builds the self-rag workflow from examples/self_rag.yaml.
func SelfRAG(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	cfg.scenarioName = "self-rag"
	cfg.instructions = "Use grade output to decide whether retrieved evidence is sufficient before answering."
	cfg.tool = minimalToolKnowledge
	for _, opt := range opts {
		opt(&cfg)
	}
	b := New(cfg.scenarioName).
		Description("Self-RAG workflow that grades retrieval before the agent synthesizes an answer with explicit relevance feedback.").
		SelfRAGChatMockLLM("test-chat").
		EmbedMockLLM("test-embed").
		KnowledgeRetrieveTool().
		Done().
		DocsKnowledgeCollection()
	ab := b.Agent(agentName).
		ChatLLM().
		KnowledgeRetrieveTool().
		Instructions(cfg.instructions)
	wf := SelfRAGWorkflow(NameKnowledgeCollectionDocs, agentName)
	return ab.FixedWorkflow(wf).Scenario()
}

// HybridResearch builds the hybrid research stack from examples/hybrid.yaml.
func HybridResearch(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	cfg.scenarioName = "hybrid-research-answer"
	cfg.instructions = "You are a research analyst. You have been provided with workflow step outputs as JSON context. Synthesise the information into a clear and concise answer. You may call repo_search again if additional detail is needed."
	cfg.tool = minimalToolRepoSearch
	for _, opt := range opts {
		opt(&cfg)
	}
	b := New(cfg.scenarioName).
		Description("Hybrid mode: a fixed workflow phase first searches a repository and collects relevant context, then an autonomous agent synthesises the findings into a final answer.").
		ResearchStack()
	ab := b.Agent(agentName).
		PrimaryLLM().
		SessionMemory().
		RepoSearchTool().
		Instructions(cfg.instructions)
	wf := HybridResearchWorkflow(NameRepoSearch)
	return ab.Hybrid(wf).Scenario()
}

// MinimalHumanInLoop builds the human-in-loop demo from examples/human_in_loop.yaml.
func MinimalHumanInLoop(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	cfg.scenarioName = "human-in-loop-demo"
	cfg.instructions = "Prepare an answer, then wait for approval before finalizing."
	for _, opt := range opts {
		opt(&cfg)
	}
	b := New(cfg.scenarioName).StandardStack()
	ab := b.Agent(agentName).StandardAgent().Instructions(cfg.instructions)
	return ab.Autonomous().BeforeFinalAnswerHITL().Scenario()
}

// MultiExpertResearch builds the multi-expert hybrid stack from examples/multi_expert_research.yaml.
func MultiExpertResearch(opts ...MinimalOption) core.Scenario {
	cfg := minimalConfig{
		scenarioName: "multi-expert-research",
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	b := New(cfg.scenarioName).
		Description("Parallel expert analysis with plan tracking and lead-author synthesis.").
		PrimaryMockLLM().
		SessionMemory().
		ExpertAgent(NameExpertMacro, "Provide macroeconomic analysis.").
		ExpertAgent(NameExpertIndustry, "Provide industry trend analysis.").
		ExpertAgent(NameExpertFinance, "Provide financial metrics analysis.")
	ab := b.Agent(NameLeadAuthor).
		PrimaryLLM().
		SessionMemory().
		Instructions("Synthesize expert outputs into an executive research brief.")
	wf := MultiExpertResearchWorkflow(NameExpertMacro, NameExpertIndustry, NameExpertFinance)
	return ab.Hybrid(wf).
		Orchestration(Planning(true, PlanningExecute(true), PlanningMaxSteps(4))).
		Scenario()
}

// CodeReviewPipeline builds the code review workflow from examples/code_review_pipeline.yaml.
func CodeReviewPipeline(opts ...MinimalOption) core.Scenario {
	cfg := minimalConfig{scenarioName: "code-review-pipeline"}
	for _, opt := range opts {
		opt(&cfg)
	}
	b := New(cfg.scenarioName).
		Description("Fixed workflow for repository diff inspection, parallel review, and maintainer approval.").
		ReviewerMockLLM().
		GitTool().
		Done()
	b.Agent(NameReviewerSecurity).
		ReviewerLLM().
		Instructions("Review changes for security risks.").
		Done()
	b.Agent(NameReviewerStyle).
		ReviewerLLM().
		Instructions("Review changes for style and readability.").
		Done()
	wf := CodeReviewPipelineWorkflow(NameGitTool, NameReviewerSecurity, NameReviewerStyle)
	return b.FixedWorkflow(wf).
		Orchestration(MaxParallel(2), HumanInLoop(true)).
		Scenario()
}

// WorkflowEnhancements builds the workflow enhancements demo from examples/workflow_enhancements.yaml.
func WorkflowEnhancements(opts ...MinimalOption) core.Scenario {
	cfg := minimalConfig{scenarioName: "workflow-enhancements"}
	for _, opt := range opts {
		opt(&cfg)
	}
	b := New(cfg.scenarioName).
		PlannerMockLLM().
		EchoTool().
		StatusTool().
		Done()
	b.Agent(NameWorkflowReviewer).
		PlannerLLM().
		EchoTool().
		Instructions("Review workflow outputs.").
		Done()
	wf := WorkflowEnhancementsWorkflow()
	return b.FixedWorkflow(wf).
		Runtime(RuntimeSecret(RuntimeSecretAPIKey, RuntimeSecretAPIKeyDevValue)).
		Scenario()
}

// ContextGovernance builds the context governance demo from examples/context_governance.yaml.
func ContextGovernance(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	cfg.scenarioName = "context-governance-demo"
	cfg.instructions = "You are a context-governance test assistant. Preserve system instructions and the latest user request."
	for _, opt := range opts {
		opt(&cfg)
	}
	b := New(cfg.scenarioName).
		ContextGovernanceLLM().
		SessionMemory()
	ab := b.Agent(agentName).
		DefaultLLM().
		SessionMemory().
		Instructions(cfg.instructions)
	return ab.Autonomous().Scenario()
}

// MinimalFixedWorkflowReview builds the fixed workflow review stack from examples/fixed_workflow.yaml.
func MinimalFixedWorkflowReview(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	cfg.scenarioName = "fixed-workflow-review"
	cfg.instructions = "Review repository changes."
	cfg.tool = minimalToolRepoSearch
	for _, opt := range opts {
		opt(&cfg)
	}
	b := New(cfg.scenarioName).
		PlannerMockLLM().
		SessionMemory().
		RepoSearchTool().
		Done()
	ab := b.Agent(agentName).
		PlannerLLM().
		SessionMemory().
		RepoSearchTool().
		Instructions(cfg.instructions)
	wf := FixedWorkflowReviewWorkflow(NameRepoSearch, agentName)
	return ab.FixedWorkflow(wf).Scenario()
}
