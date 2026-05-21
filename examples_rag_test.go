package agentflow_test

import (
	"testing"

	"github.com/aijustin/agentflow-go"
)

func TestLoadAgenticRAGExamples(t *testing.T) {
	for _, path := range []string{
		"examples/corrective_rag.yaml",
		"examples/self_rag.yaml",
		"examples/adaptive_rag.yaml",
	} {
		if _, err := agentflow.LoadScenarioFile(path); err != nil {
			t.Fatalf("load %s: %v", path, err)
		}
	}
}
