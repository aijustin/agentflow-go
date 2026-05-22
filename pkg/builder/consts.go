package builder

import (
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/memory"
)

// Conventional scenario resource names used across examples and tests.
const (
	NameDefaultLLM              = "default"
	NamePrimaryLLM              = "primary"
	NameChatLLM                 = "chat"
	NameEmbedLLM                = "embed"
	NameSessionMemory           = "session"
	NameEchoTool                = "echo"
	NameRepoSearch              = "repo_search"
	NameGitTool                 = "git"
	NameTicketTool              = "ticket"
	NameSQLTool                 = "sql"
	NameFilesystemTool          = "filesystem"
	NameHTTPTool                = "http"
	NameHTTPToolStatus          = "http.status"
	NameSQLQueryTool            = "sql.query"
	NameFilesystemReadTool      = "fs.read"
	NameMCPSearchTool           = "docs.search"
	NameMCPServerDocs           = "docs"
	NameKnowledgeRetrieveTool   = "knowledge.retrieve"
	NameKnowledgeCollectionDocs = "docs"
	NameExpertMacro             = "macro"
	NameExpertIndustry          = "industry"
	NameExpertFinance           = "finance"
	NameLeadAuthor              = "lead_author"
	NameReviewerLLM             = "reviewer"
	NameReviewerSecurity        = "security"
	NameReviewerStyle           = "style"
	NamePlannerLLM              = "planner"
	NameStatusTool              = "status"
	NameWorkflowReviewer        = "reviewer"
)

// Memory backend types accepted by scenario YAML.
const (
	MemoryTypeInMemory = "in_memory"
	MemoryTypeFile     = "file"
	MemoryTypeCustom   = "custom"
)

// Memory scopes accepted by scenario YAML.
const (
	MemoryScopeConversation = string(memory.ScopeConversation)
	MemoryScopeSession      = string(memory.ScopeSession)
	MemoryScopeLongTerm     = string(memory.ScopeLongTerm)
	MemoryScopeAudit        = string(memory.ScopeAudit)
)

// LLM provider names commonly used in scenarios.
const (
	LLMProviderMock         = "mock"
	LLMProviderOpenAICompat = "openai-compatible"
	LLMProviderAnthropic    = "anthropic"
	LLMProviderOpenAI       = "openai"
	LLMModelMockTest        = "test"
)

// Builtin and declared tool types accepted by scenario YAML.
const (
	ToolTypeEcho               = "builtin.echo"
	ToolTypeRepoSearch         = "builtin.repo_search"
	ToolTypeGit                = "builtin.git"
	ToolTypeTicket             = "builtin.ticket"
	ToolTypeFilesystem         = "builtin.filesystem"
	ToolTypeHTTP               = "builtin.http"
	ToolTypeHTTPClient         = "http.client"
	ToolTypeSQL                = "builtin.sql"
	ToolTypeKnowledgeRetriever = "knowledge.retriever"
	ToolTypeMCPTool            = "mcp.tool"
)

// MCP transport and metadata keys used in examples.
const (
	MCPTransportHTTP        = "http"
	MCPTransportStdio       = "stdio"
	MCPServerURLDocsDefault = "http://127.0.0.1:3333/mcp"
	MCPToolPrefixDocs       = "docs"
	MCPMetadataServer       = "mcp_server"
	MCPMetadataTool         = "mcp_tool"
	MCPToolSearch           = "search"
)

// Default tool rate caps from examples.
const (
	RateCapHTTPToolDefault       = 5
	RateCapSQLToolDefault        = 5
	RateCapFilesystemToolDefault = 10
)

// LLM capability strings accepted by scenario YAML.
const (
	LLMCapChat             = "chat"
	LLMCapToolCall         = "tool_call"
	LLMCapStructuredOutput = "structured_output"
	LLMCapStream           = "stream"
	LLMCapEmbed            = "embed"
)

// Knowledge search modes accepted by scenario YAML.
const (
	KnowledgeSearchModeVector = "vector"
	KnowledgeSearchModeHybrid = "hybrid"
)

// Human-in-the-loop checkpoint names used by runtime.
const (
	CheckpointBeforeFinalAnswer = "before_final_answer"
)

// Common trigger event types from examples.
const (
	EventTicketCreated = "ticket.created"
)

// Common JSONPath fields for trigger payload mapping.
const (
	TriggerPathTicketSummary = "body.summary"
	TriggerPathTicketBody    = "body"
	TriggerPathTicketID      = "body.ticket_id"
)

// Context governance strategy strings accepted by scenario YAML.
const (
	ContextStrategySlidingWindowWithSummary = string(contextwindow.StrategySlidingWindowWithSummary)
	ContextSummaryModeLLM                   = "llm"
)

// Common RAG workflow conditions.
const (
	ConditionRouteToRAG       = `eq(steps.route.output.route, "rag")`
	ConditionGradeNotRelevant = `eq(steps.grade.output.relevant, false)`
	RAGRetrieveLimitDefault   = 5
	RAGGradeMinScoreDefault   = 0.35
)

// Parallel group on_error strategies accepted by scenario YAML.
const (
	ParallelOnErrorCollectErrors = "collect_errors"
	ParallelOnErrorFailFast      = "fail_fast"
)

// Common workflow edge conditions.
const (
	ConditionStatusReady = `eq(steps.status.output.message, "ready")`
)

// Runtime secret keys used in examples.
const (
	RuntimeSecretAPIKey         = "api_key"
	RuntimeSecretAPIKeyDevValue = "dev-secret-value"
)

// Context governance LLM defaults for catalog ID context-governance.
const (
	LLMEnvRealModelAPIKey        = "AGENT_REALMODEL_API_KEY"
	LLMModelQwen35B              = "qwen/qwen3.6-35b-a3b"
	LLMEndpointLocalOpenAICompat = "http://127.0.0.1:1234/v1"
)

// Re-export typed enums from core so builder callers avoid importing two packages
// for the same approval, side-effect, and orchestration constants.
const (
	ApprovalNever  = core.ApprovalNever
	ApprovalRisky  = core.ApprovalRisky
	ApprovalAlways = core.ApprovalAlways
	ApprovalPause  = core.ApprovalPause

	SideEffectNone      = core.SideEffectNone
	SideEffectRead      = core.SideEffectRead
	SideEffectWrite     = core.SideEffectWrite
	SideEffectExternal  = core.SideEffectExternal
	SideEffectDangerous = core.SideEffectDangerous

	ModeAutonomous    = core.OrchestrationAutonomous
	ModeFixedWorkflow = core.OrchestrationFixedWorkflow
	ModeHybrid        = core.OrchestrationHybrid

	NodeAgent         = core.NodeAgent
	NodeTool          = core.NodeTool
	NodeSkill         = core.NodeSkill
	NodeHumanGate     = core.NodeHumanGate
	NodeTransform     = core.NodeTransform
	NodeParallelGroup = core.NodeParallelGroup
	NodeLoop          = core.NodeLoop
	NodeQueryRouter   = core.NodeQueryRouter
	NodeRAGGrade      = core.NodeRAGGrade
	NodeSupervisor    = core.NodeSupervisor
	NodeSubgraph      = core.NodeSubgraph
	NodeMap           = core.NodeMap
)
