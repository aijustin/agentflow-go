package skill

import (
	"context"
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// ScriptRuntime executes script-kind skills. Implementations are optional;
// prompt-kind skills do not require a runtime.
type ScriptRuntime interface {
	Execute(ctx context.Context, skill core.Skill, input json.RawMessage) (json.RawMessage, error)
}
