package core

// KnowledgeCollection binds agents to a retriever tool and vector namespace.
type KnowledgeCollection struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Namespace    string   `json:"namespace"`
	Tool         string   `json:"tool,omitempty"`
	EmbedProfile string   `json:"embed_profile,omitempty"`
	SearchMode   string   `json:"search_mode,omitempty"`
	Agents       []string `json:"agents,omitempty"`
	TenantScoped bool     `json:"tenant_scoped,omitempty"`
}
