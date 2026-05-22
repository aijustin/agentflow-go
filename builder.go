package agentflow

import scenariobuilder "github.com/aijustin/agentflow-go/pkg/builder"

// ScenarioBuilder constructs scenarios with a fluent Go API.
// See pkg/builder for the full surface.
type ScenarioBuilder = scenariobuilder.ScenarioBuilder

// WorkflowBuilder constructs workflow graphs with a fluent Go API.
type WorkflowBuilder = scenariobuilder.WorkflowBuilder

// NewScenarioBuilder creates a scenario builder. Prefer pkg/builder when
// importing only the builder package without the root facade.
var NewScenarioBuilder = scenariobuilder.New

// NewWorkflowBuilder creates a workflow builder.
var NewWorkflowBuilder = scenariobuilder.NewWorkflow

// MinimalAutonomous builds the common mock/session/tool autonomous stack.
var MinimalAutonomous = scenariobuilder.MinimalAutonomous

// NewMinimal starts a scenario with the standard mock/session stack. Register
// tools, then call MinimalAgent or configure agents manually.
var NewMinimal = scenariobuilder.NewMinimal

// MinimalTicketHandling builds the ticket-handling example stack.
var MinimalTicketHandling = scenariobuilder.MinimalTicketHandling

// MinimalRAG builds the rag-knowledge example stack.
var MinimalRAG = scenariobuilder.MinimalRAG

// TierMemoryAutonomous builds the tier-memory example stack.
var TierMemoryAutonomous = scenariobuilder.TierMemoryAutonomous

// AdaptiveRAG builds the adaptive-rag workflow example stack.
var AdaptiveRAG = scenariobuilder.AdaptiveRAG

// CorrectiveRAG builds the corrective-rag workflow example stack.
var CorrectiveRAG = scenariobuilder.CorrectiveRAG

// SelfRAG builds the self-rag workflow example stack.
var SelfRAG = scenariobuilder.SelfRAG

// HybridResearch builds the hybrid research example stack.
var HybridResearch = scenariobuilder.HybridResearch

// MinimalHumanInLoop builds the human-in-loop demo stack.
var MinimalHumanInLoop = scenariobuilder.MinimalHumanInLoop

// MultiExpertResearch builds the multi-expert hybrid example stack.
var MultiExpertResearch = scenariobuilder.MultiExpertResearch

// CodeReviewPipeline builds the code review workflow example stack.
var CodeReviewPipeline = scenariobuilder.CodeReviewPipeline

// WorkflowEnhancements builds the workflow enhancements example stack.
var WorkflowEnhancements = scenariobuilder.WorkflowEnhancements

// ContextGovernance builds the context governance example stack.
var ContextGovernance = scenariobuilder.ContextGovernance

// MinimalFixedWorkflowReview builds the fixed workflow review example stack.
var MinimalFixedWorkflowReview = scenariobuilder.MinimalFixedWorkflowReview

// MinimalHTTPTool builds the http tool example stack.
var MinimalHTTPTool = scenariobuilder.MinimalHTTPTool

// MinimalSQLTool builds the sql tool example stack.
var MinimalSQLTool = scenariobuilder.MinimalSQLTool

// MinimalFilesystemTool builds the filesystem tool example stack.
var MinimalFilesystemTool = scenariobuilder.MinimalFilesystemTool

// MinimalMCPTool builds the MCP tool example stack.
var MinimalMCPTool = scenariobuilder.MinimalMCPTool

// AdaptiveRAGWorkflow builds the adaptive RAG workflow graph.
var AdaptiveRAGWorkflow = scenariobuilder.AdaptiveRAGWorkflow

// CorrectiveRAGWorkflow builds the corrective RAG workflow graph.
var CorrectiveRAGWorkflow = scenariobuilder.CorrectiveRAGWorkflow

// SelfRAGWorkflow builds the self RAG workflow graph.
var SelfRAGWorkflow = scenariobuilder.SelfRAGWorkflow

// HybridResearchWorkflow builds the hybrid research workflow graph.
var HybridResearchWorkflow = scenariobuilder.HybridResearchWorkflow

// MultiExpertResearchWorkflow builds the parallel expert workflow graph.
var MultiExpertResearchWorkflow = scenariobuilder.MultiExpertResearchWorkflow

// CodeReviewPipelineWorkflow builds the code review workflow graph.
var CodeReviewPipelineWorkflow = scenariobuilder.CodeReviewPipelineWorkflow

// WorkflowEnhancementsWorkflow builds the workflow enhancements graph.
var WorkflowEnhancementsWorkflow = scenariobuilder.WorkflowEnhancementsWorkflow

// FixedWorkflowReviewWorkflow builds the inspect → review workflow graph.
var FixedWorkflowReviewWorkflow = scenariobuilder.FixedWorkflowReviewWorkflow

// MapBranch configures a map node fan-out branch.
type MapBranch = scenariobuilder.MapBranch

// MapNodeInput builds map node input JSON from a list field and branches.
var MapNodeInput = scenariobuilder.MapNodeInput

// MapOnError sets the map node error policy branch.
var MapOnError = scenariobuilder.MapOnError

// MapItemField sets the per-item field name for map branches.
var MapItemField = scenariobuilder.MapItemField
