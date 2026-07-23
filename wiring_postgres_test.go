package agentflow_test

import (
	"context"
	"encoding/json"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/adapters"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/identity"
)

func TestPostgresWrapperValidation(t *testing.T) {
	if _, err := adapters.NewPostgresJobQueue(nil, "jobs", "extra"); err == nil {
		t.Fatal("expected too many table names error")
	}
	if _, err := adapters.NewPostgresJobQueue(nil); err == nil {
		t.Fatal("expected nil db error")
	}
	if _, err := adapters.NewPostgresCheckpointHistory(nil, "history", "extra"); err == nil {
		t.Fatal("expected too many checkpoint table names error")
	}
	if _, err := adapters.NewPostgresCheckpointHistory(nil); err == nil {
		t.Fatal("expected nil db error for checkpoint history")
	}
	if _, err := adapters.NewPostgresRunStateRepository(nil, "runs", "extra"); err == nil {
		t.Fatal("expected too many runstate table names error")
	}
}

func TestAsyncJobHandlerRejectsInvalidPayloads(t *testing.T) {
	fw, err := agentflow.New(
		builder.MinimalAutonomous("assistant"),
		agentflow.WithLLMGateway(fakeGateway{content: "ok"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := handler.HandleJob(ctx, asyncpkg.Job{Type: asyncpkg.EventJobType, Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("expected decode error for event job")
	}
	if err := handler.HandleJob(ctx, asyncpkg.Job{Type: asyncpkg.ResumeContinueJobType, Payload: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("expected missing token error")
	}
	if err := handler.HandleJob(ctx, asyncpkg.Job{Type: asyncpkg.MemoryReconcileJobType, Payload: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("expected missing memory fields error")
	}
	invalidPrincipal, _ := json.Marshal(asyncpkg.RunPayload{
		RunID: "run-1", Agent: "assistant", Prompt: "hi",
		Principal: identity.Principal{ID: "bad"},
	})
	if err := handler.HandleJob(ctx, asyncpkg.Job{Type: asyncpkg.RunJobType, Payload: invalidPrincipal}); err == nil {
		t.Fatal("expected invalid principal error")
	}
}

func TestFrameworkTierOptionValidation(t *testing.T) {
	scenario := builder.MinimalAutonomous("assistant")
	if _, err := agentflow.New(scenario, agentflow.WithTierColdSummarizer("", adapters.NewLLMTierSummarizer(fakeGateway{content: "s"}, "default"))); err == nil {
		t.Fatal("expected missing memory name error")
	}
	if _, err := agentflow.New(scenario, agentflow.WithTierColdSummarizer("session", nil)); err == nil {
		t.Fatal("expected nil summarizer error")
	}
	if _, err := agentflow.New(scenario, agentflow.WithCognitiveMemory("", nil)); err == nil {
		t.Fatal("expected missing cognitive memory name error")
	}
	if _, err := agentflow.New(scenario, agentflow.WithCognitiveMemory("session", nil)); err == nil {
		t.Fatal("expected nil cognitive repo error")
	}
}
