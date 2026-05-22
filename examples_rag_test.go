package agentflow_test

import (
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestAgenticRAGBuilderStacksValidate(t *testing.T) {
	for _, scenario := range []core.Scenario{
		builder.CorrectiveRAG("assistant"),
		builder.SelfRAG("assistant"),
		builder.AdaptiveRAG("assistant"),
	} {
		if err := agentflow.ValidateScenario(scenario); err != nil {
			t.Fatal(err)
		}
	}
}
