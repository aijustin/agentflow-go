package agentflow

import (
	"context"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestFrameworkPurgeRuns(t *testing.T) {
	repo := runstateinmem.NewRepository()
	fw, err := New(core.Scenario{
		Name:   "purge-test",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant", LLM: "mock"}},
		LLMs:   map[string]core.LLMProfileRef{"mock": {Provider: "mock", Model: "test"}},
	}, WithRunStateRepository(repo))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-1", ScenarioName: "purge-test", Status: runstate.RunStatusCompleted,
	}, 0); err != nil {
		t.Fatal(err)
	}
	removed, err := fw.PurgeRuns(context.Background(), runstate.ListFilter{ScenarioName: "purge-test"})
	if err != nil || removed != 1 {
		t.Fatalf("expected one purge, got %d err=%v", removed, err)
	}
}
