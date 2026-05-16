//go:build integration

package integration

import (
	"bytes"
	"context"
	"path/filepath"
	stdruntime "runtime"
	"testing"

	configyaml "github.com/aijustin/agentflow-go/internal/adapter/config/yaml"
	humancli "github.com/aijustin/agentflow-go/internal/adapter/human/cli"
	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	"github.com/aijustin/agentflow-go/internal/adapter/registry"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/internal/adapter/tool/builtin"
	"github.com/aijustin/agentflow-go/internal/application/orchestration"
	appexec "github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestAutonomousScenarioRuntimeIntegration(t *testing.T) {
	scenario := loadExample(t, "autonomous.yaml")
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Content: "hello from llm"}})
	engine, err := appexec.NewEngine(scenario, appexec.Dependencies{Runs: repo, LLM: gateway})
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.Run(context.Background(), appexec.RunRequest{RunID: "run-auto", Agent: "assistant", Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "hello from llm" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestHumanInLoopPauseResumeIntegration(t *testing.T) {
	scenario := loadExample(t, "human_in_loop.yaml")
	repo := runstateinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("integration-secret"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	gate := humancli.NewGate(repo, signer, &out)
	engine, err := appexec.NewEngine(scenario, appexec.Dependencies{Runs: repo, HumanGate: gate})
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.Run(context.Background(), appexec.RunRequest{RunID: "run-hitl", Agent: "assistant", Prompt: "needs approval"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused || result.Token == "" {
		t.Fatalf("unexpected pause result: %+v", result)
	}
	if err := gate.Resume(context.Background(), result.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.Load(context.Background(), "run-hitl")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != runstate.RunStatusRunning || loaded.PendingGate != nil {
		t.Fatalf("unexpected resumed snapshot: %+v", loaded)
	}
}

func TestFixedWorkflowIntegration(t *testing.T) {
	scenario := loadExample(t, "fixed_workflow.yaml")
	repo := runstateinmem.NewRepository()
	snapshot := runstate.RunSnapshot{RunID: "run-workflow", ScenarioName: scenario.Name, Status: runstate.RunStatusRunning}
	if err := repo.Save(context.Background(), &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.RegisterTool("repo_search", builtin.NewEchoTool()); err != nil {
		t.Fatal(err)
	}
	runner := orchestration.NewWorkflowRunner(reg, repo, nil)
	if err := runner.Run(context.Background(), scenario, "run-workflow"); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.Load(context.Background(), "run-workflow")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.StepOutputs["inspect"]; !ok {
		t.Fatalf("expected inspect output: %+v", loaded.StepOutputs)
	}
}

func loadExample(t *testing.T, name string) core.Scenario {
	t.Helper()
	_, file, _, ok := stdruntime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	scenario, err := configyaml.LoadFile(filepath.Join(root, "examples", name))
	if err != nil {
		t.Fatal(err)
	}
	return scenario
}
