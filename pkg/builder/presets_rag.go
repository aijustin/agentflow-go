package builder

import (
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// knowledgeRetrieveInputSchema matches catalog ID rag-knowledge.
var knowledgeRetrieveInputSchema = json.RawMessage(`{"type":"object","required":["query"],"properties":{"query":{"type":"string"},"namespace":{"type":"string"},"limit":{"type":"integer"}}}`)

// MockChatLLM configures a mock chat LLM profile.
func MockChatLLM(model string) LLMOption {
	return func(p *core.LLMProfileRef) {
		p.Provider = LLMProviderMock
		p.Model = model
		p.Capabilities = []string{LLMCapChat, LLMCapToolCall}
	}
}

// SelfRAGChatMockLLM configures the self_rag chat profile with context governance.
func SelfRAGChatMockLLM(model string) LLMOption {
	return func(p *core.LLMProfileRef) {
		MockChatLLM(model)(p)
		SelfRAGChatContext()(p)
	}
}

// MockEmbedLLM configures a mock embed LLM profile.
func MockEmbedLLM(model string) LLMOption {
	return func(p *core.LLMProfileRef) {
		p.Provider = LLMProviderMock
		p.Model = model
		p.Capabilities = []string{LLMCapEmbed}
	}
}

// KnowledgeRetrieveToolPreset configures the knowledge retriever tool.
func KnowledgeRetrieveToolPreset() ToolOption {
	return func(t *core.Tool) {
		t.Type = ToolTypeKnowledgeRetriever
		t.Description = "Retrieve relevant documents from the configured knowledge vector store."
		t.Approval = ApprovalNever
		t.SideEffect = SideEffectRead
		t.RateCap = 8
		t.InputSchema = knowledgeRetrieveInputSchema
	}
}

// DocsKnowledgeCollectionPreset configures the conventional docs collection.
func DocsKnowledgeCollectionPreset() KnowledgeCollectionOption {
	return func(c *core.KnowledgeCollection) {
		c.Name = NameKnowledgeCollectionDocs
		c.Namespace = NameKnowledgeCollectionDocs
		c.Tool = NameKnowledgeRetrieveTool
		c.EmbedProfile = NameEmbedLLM
		c.SearchMode = KnowledgeSearchModeHybrid
	}
}

// TicketCreatedTriggerPreset configures the ticket.created trigger from examples.
func TicketCreatedTriggerPreset(agentName string) []TriggerOption {
	return []TriggerOption{
		TriggerEvent(EventTicketCreated),
		TriggerAgent(agentName),
		TriggerPromptPath(TriggerPathTicketSummary),
		TriggerContextPath(TriggerPathTicketBody),
		TriggerRunIDPath(TriggerPathTicketID),
	}
}

// ChatMockLLM registers the conventional chat LLM profile.
func (b *ScenarioBuilder) ChatMockLLM(model string) *ScenarioBuilder {
	return b.LLM(NameChatLLM, MockChatLLM(model))
}

// SelfRAGChatMockLLM registers the self_rag chat profile with context governance.
func (b *ScenarioBuilder) SelfRAGChatMockLLM(model string) *ScenarioBuilder {
	return b.LLM(NameChatLLM, SelfRAGChatMockLLM(model))
}

// ResearchStack registers primary LLM, session memory, and repo_search tool.
func (b *ScenarioBuilder) ResearchStack() *ScenarioBuilder {
	return b.
		PrimaryMockLLM().
		SessionMemory().
		RepoSearchTool().
		Done()
}

// EmbedMockLLM registers the conventional embed LLM profile.
func (b *ScenarioBuilder) EmbedMockLLM(model string) *ScenarioBuilder {
	return b.LLM(NameEmbedLLM, MockEmbedLLM(model))
}

// ReviewerMockLLM registers the code-review reviewer LLM profile.
func (b *ScenarioBuilder) ReviewerMockLLM() *ScenarioBuilder {
	return b.LLM(NameReviewerLLM, MockLLM())
}

// PrimaryMockLLM registers the conventional primary LLM profile.
func (b *ScenarioBuilder) PrimaryMockLLM() *ScenarioBuilder {
	return b.LLM(NamePrimaryLLM, MockLLM())
}

// ExpertAgent registers a primary-LLM expert with instructions.
func (b *ScenarioBuilder) ExpertAgent(name, instructions string) *ScenarioBuilder {
	b.Agent(name).PrimaryLLM().Instructions(instructions).Done()
	return b
}

// TierSessionMemory registers tiered custom session memory.
func (b *ScenarioBuilder) TierSessionMemory(namespace string, opts ...TierMemoryOption) *ScenarioBuilder {
	return b.Memory(NameSessionMemory, TierSessionMemory(namespace, opts...))
}

// KnowledgeRetrieveTool registers the knowledge retriever tool.
func (b *ScenarioBuilder) KnowledgeRetrieveTool() *ToolBuilder {
	return b.Tool(NameKnowledgeRetrieveTool, KnowledgeRetrieveToolPreset())
}

// DocsKnowledgeCollection registers the docs knowledge collection.
func (b *ScenarioBuilder) DocsKnowledgeCollection(opts ...KnowledgeCollectionOption) *ScenarioBuilder {
	b.KnowledgeCollection(DocsKnowledgeCollectionPreset())
	if len(opts) == 0 {
		return b
	}
	last := len(b.s.Knowledge.Collections) - 1
	for _, opt := range opts {
		opt(&b.s.Knowledge.Collections[last])
	}
	return b
}

// TicketCreatedTrigger registers the ticket.created trigger for an agent.
func (b *ScenarioBuilder) TicketCreatedTrigger(agentName string) *ScenarioBuilder {
	return b.Trigger(TicketCreatedTriggerPreset(agentName)...)
}

// BeforeFinalAnswerHITL enables before_final_answer human approval.
func (b *ScenarioBuilder) BeforeFinalAnswerHITL() *ScenarioBuilder {
	return b.Orchestration(HumanInLoop(true, CheckpointBeforeFinalAnswer))
}

// ChatLLM wires the agent to NameChatLLM.
func (ab *AgentBuilder) ChatLLM() *AgentBuilder {
	return ab.LLM(NameChatLLM)
}

// PrimaryLLM wires the agent to NamePrimaryLLM.
func (ab *AgentBuilder) PrimaryLLM() *AgentBuilder {
	return ab.LLM(NamePrimaryLLM)
}

// KnowledgeRetrieveTool appends the knowledge retriever tool reference.
func (ab *AgentBuilder) KnowledgeRetrieveTool() *AgentBuilder {
	return ab.Tool(NameKnowledgeRetrieveTool)
}

// BeforeFinalAnswerCheckpoint appends the before_final_answer checkpoint.
func (ab *AgentBuilder) BeforeFinalAnswerCheckpoint() *AgentBuilder {
	return ab.HumanCheckpoint(CheckpointBeforeFinalAnswer)
}

// RAGStack registers chat/embed LLMs, retriever tool, and docs collection.
func (b *ScenarioBuilder) RAGStack() *ScenarioBuilder {
	return b.
		ChatMockLLM("test-chat").
		EmbedMockLLM("test-embed").
		KnowledgeRetrieveTool().
		Done().
		DocsKnowledgeCollection()
}

// StandardTicketStack registers primary LLM, session memory, and ticket tool.
func (b *ScenarioBuilder) StandardTicketStack() *ScenarioBuilder {
	return b.
		PrimaryMockLLM().
		SessionMemory().
		TicketTool().
		Done()
}
