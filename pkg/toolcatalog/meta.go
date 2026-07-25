package toolcatalog

import "encoding/json"

const (
	// ToolSearchTools is the built-in meta-tool for keyword catalog search.
	ToolSearchTools = "search_tools"
	// ToolLoadSchemas is the built-in meta-tool for loading deferred schemas.
	ToolLoadSchemas = "load_tool_schemas"
	// ToolCompactContext signals the end of a sub-task so the runtime can
	// compact masked tool observations before the next LLM turn.
	ToolCompactContext = "compact_context"
)

var (
	searchToolsSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "Keyword query matched against tool name, description, and tags"},
    "limit": {"type": "integer", "minimum": 1, "description": "Maximum number of results (default 10)"}
  },
  "required": ["query"]
}`)

	loadToolSchemasSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "names": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Tool names to load full schemas for"
    }
  },
  "required": ["names"]
}`)

	compactContextSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "reason": {
      "type": "string",
      "description": "Optional note describing the completed sub-task"
    }
  }
}`)
)

// SelfCompactMetaToolSpec returns the compact_context meta-tool spec.
func SelfCompactMetaToolSpec() MetaToolSpec {
	return MetaToolSpec{
		Name:        ToolCompactContext,
		Description: "Signal that a sub-task is complete. The runtime compacts older tool observations before the next model turn to free context window space.",
		Schema:      compactContextSchema,
	}
}

// SelfCompactRubric returns a short instruction hosts can append to agent
// system prompts when compact_context is available.
func SelfCompactRubric() string {
	return "When you finish a self-contained sub-task, call compact_context so prior tool observations can be dropped before continuing."
}

// MetaToolSpecs returns LLM tool specs for catalog meta-tools.
func MetaToolSpecs() []MetaToolSpec {
	return []MetaToolSpec{
		{
			Name:        ToolSearchTools,
			Description: "Search the deferred tool catalog by keyword. Returns lightweight matches; call load_tool_schemas to inject full schemas.",
			Schema:      searchToolsSchema,
		},
		{
			Name:        ToolLoadSchemas,
			Description: "Load full input schemas for named tools from the catalog so they become callable in subsequent turns.",
			Schema:      loadToolSchemasSchema,
		},
	}
}

// MetaToolSpec is a lightweight spec helper for catalog meta-tools.
type MetaToolSpec struct {
	Name        string
	Description string
	Schema      json.RawMessage
}
