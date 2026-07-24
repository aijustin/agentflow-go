package agentflow_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

func TestValidateWiringRequiresToolExecutor(t *testing.T) {
	scenario := core.Scenario{
		Name: "wiring-test",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Tools: map[string]core.Tool{
			"http_call": {Name: "http_call", Type: "http.client"},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	err := agentflow.ValidateWiring(scenario)
	if err == nil {
		t.Fatal("expected missing tool executor error")
	}
}

func TestValidateWiringTestOptions(t *testing.T) {
	scenario := builder.MinimalAutonomous("assistant")
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: "."})
	if err != nil {
		t.Fatal(err)
	}
	if err := agentflow.ValidateWiring(scenario, opts...); err != nil {
		t.Fatal(err)
	}
}

func TestWithRequireLLM(t *testing.T) {
	scenario := builder.MinimalAutonomous("assistant")
	_, err := agentflow.New(scenario, agentflow.WithRequireLLM())
	if err == nil {
		t.Fatal("expected require LLM error")
	}
}

func TestFrameworkNewValidatesMemoryWiring(t *testing.T) {
	scenario := core.Scenario{
		Name: "wiring-test",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Memories: map[string]core.MemoryRef{
			"postgres_mem": {Type: "postgres"},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	_, err := agentflow.New(scenario, agentflow.WithToolExecutor("echo", noopTool{}))
	if err == nil {
		t.Fatal("expected missing memory repository error from Framework.New")
	}
}

func TestScenarioJSONSchema(t *testing.T) {
	schema := agentflow.ScenarioJSONSchema()
	if len(schema) == 0 {
		t.Fatal("expected non-empty schema")
	}
}

// schemaVersionRepo stubs a PostgreSQL-like run-state repository: it reports
// a schema version the way the postgres adapter's SchemaVersion does, so the
// wiring check can be tested without a database.
type schemaVersionRepo struct {
	version int
	err     error
}

func (r schemaVersionRepo) Save(context.Context, *runstate.RunSnapshot, int64) error {
	return nil
}
func (r schemaVersionRepo) Load(context.Context, string) (runstate.RunSnapshot, error) {
	return runstate.RunSnapshot{}, runstate.ErrNotFound
}
func (r schemaVersionRepo) Delete(context.Context, string) error { return nil }
func (r schemaVersionRepo) List(context.Context, runstate.ListFilter) ([]runstate.RunSnapshot, error) {
	return nil, nil
}
func (r schemaVersionRepo) SchemaVersion(context.Context) (int, error) { return r.version, r.err }

func TestValidateWiringRequiresMinimumSchemaVersion(t *testing.T) {
	scenario := builder.MinimalAutonomous("assistant")
	if err := agentflow.ValidateWiring(scenario, agentflow.WithRunStateRepository(schemaVersionRepo{version: 5})); err != nil {
		t.Fatalf("schema version 5 must pass wiring: %v", err)
	}
	err := agentflow.ValidateWiring(scenario, agentflow.WithRunStateRepository(schemaVersionRepo{version: 4}))
	if err == nil {
		t.Fatal("expected schema version error for version 4")
	}
	if !strings.Contains(err.Error(), "migrations") {
		t.Fatalf("error must point at the migrations, got %v", err)
	}
	if err := agentflow.ValidateWiring(scenario, agentflow.WithRunStateRepository(schemaVersionRepo{version: 0})); err == nil {
		t.Fatal("expected schema version error for a never-migrated database (version 0)")
	}
	if err := agentflow.ValidateWiring(scenario, agentflow.WithRunStateRepository(schemaVersionRepo{err: errors.New("connection refused")})); err == nil {
		t.Fatal("expected schema version query failure to surface")
	}
}
