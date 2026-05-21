package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aijustin/agentflow-go/internal/application/orchestration"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

const (
	resumePromptVar     = "resume_prompt"
	resumeAgentVar      = "resume_agent"
	executionPhaseVar   = "execution_phase"
	executionPhaseWorkflow   = "workflow"
	executionPhaseAutonomous = "autonomous"
)

// ResumeAndContinue resumes a paused run and continues execution until the next
// pause point or completion.
func (f *Framework) ResumeAndContinue(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) (RunResult, error) {
	if f.gate == nil {
		return RunResult{}, fmt.Errorf("agentflow: human gate is not configured")
	}
	runID, err := f.runIDFromToken(token)
	if err != nil {
		return RunResult{}, err
	}
	if err := f.gate.Resume(ctx, token, decision, amendment); err != nil {
		return RunResult{}, err
	}
	if decision == core.DecisionReject {
		return RunResult{RunID: runID, Status: runstate.RunStatusCancelled}, nil
	}
	return f.continueRun(ctx, runID)
}

func (f *Framework) runIDFromToken(token string) (string, error) {
	if f.tokenSigner == nil {
		return "", fmt.Errorf("agentflow: token signer is not configured")
	}
	payload, err := f.tokenSigner.Verify(token)
	if err != nil {
		return "", err
	}
	return payload.RunID, nil
}

func (f *Framework) continueRun(ctx context.Context, runID string) (RunResult, error) {
	snapshot, err := f.runs.Load(ctx, runID)
	if err != nil {
		return RunResult{}, err
	}
	switch f.scenario.Orchestration.Mode {
	case core.OrchestrationFixedWorkflow:
		return f.continueWorkflowRun(ctx, runID)
	case core.OrchestrationHybrid:
		return f.continueHybridRun(ctx, runID, snapshot)
	default:
		return f.engine.ContinueAfterCheckpoint(ctx, runID)
	}
}

func (f *Framework) continueWorkflowRun(ctx context.Context, runID string) (RunResult, error) {
	runner := f.newWorkflowRunner()
	if err := runner.Resume(ctx, f.scenario, runID); err != nil {
		var paused orchestration.WorkflowPausedError
		if errors.As(err, &paused) {
			return RunResult{RunID: runID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		f.markWorkflowFailed(ctx, runID, err)
		return RunResult{}, err
	}
	loaded, err := f.runs.Load(ctx, runID)
	if err != nil {
		return RunResult{}, err
	}
	loaded.Status = runstate.RunStatusCompleted
	if err := f.runs.Save(ctx, &loaded, loaded.Version); err != nil {
		return RunResult{}, err
	}
	f.emit(ctx, core.EventRunCompleted, runID, nil)
	return RunResult{RunID: runID, Status: runstate.RunStatusCompleted, Output: "fixed workflow completed"}, nil
}

func (f *Framework) continueHybridRun(ctx context.Context, runID string, snapshot runstate.RunSnapshot) (RunResult, error) {
	if result, ok := completedHybridResult(snapshot); ok {
		return result, nil
	}
	var err error
	if variableJSONString(snapshot.Variables, executionPhaseVar) != executionPhaseAutonomous {
		result, err := f.continueWorkflowRun(ctx, runID)
		if err != nil || result.Status == runstate.RunStatusPaused {
			return result, err
		}
		snapshot, err = f.runs.Load(ctx, runID)
		if err != nil {
			return RunResult{}, err
		}
		if snapshot.Variables == nil {
			snapshot.Variables = make(map[string]json.RawMessage)
		}
		snapshot.Variables[executionPhaseVar] = json.RawMessage(fmt.Sprintf("%q", executionPhaseAutonomous))
		if err := f.runs.Save(ctx, &snapshot, snapshot.Version); err != nil {
			return RunResult{}, err
		}
	}
	req := hybridRunRequest(snapshot)
	snapshot, err = f.runs.Load(ctx, runID)
	if err != nil {
		return RunResult{}, err
	}
	req, err = f.hydrateRunRequest(ctx, req, snapshot)
	if err != nil {
		return RunResult{}, err
	}
	return f.engine.RunHybrid(ctx, req)
}

func (f *Framework) hybridWorkflowPending(snapshot runstate.RunSnapshot) bool {
	return variableJSONString(snapshot.Variables, executionPhaseVar) == executionPhaseWorkflow
}

func hybridRunRequest(snapshot runstate.RunSnapshot) RunRequest {
	return RunRequest{
		RunID:   snapshot.RunID,
		Agent:   variableJSONString(snapshot.Variables, resumeAgentVar),
		Prompt:  variableJSONString(snapshot.Variables, resumePromptVar),
		Context: snapshot.Variables["input"],
	}
}

func (f *Framework) newWorkflowRunner() *orchestration.WorkflowRunner {
	return orchestration.NewWorkflowRunner(
		f.tools,
		f.runs,
		f.events,
		orchestration.WithAgentRegistry(workflowAgentRegistry{agents: f.scenario.Agents, engine: f.engine}),
		orchestration.WithHumanGate(f.gate),
		orchestration.WithBlobStore(f.blobs),
		orchestration.WithWorkflowToolPolicy(f.toolGov),
		orchestration.WithOutputRedactor(f.redactor),
	)
}

func saveRunResumeMetadata(snapshot *runstate.RunSnapshot, req RunRequest) {
	if snapshot.Variables == nil {
		snapshot.Variables = make(map[string]json.RawMessage)
	}
	if req.Prompt != "" {
		snapshot.Variables[resumePromptVar] = json.RawMessage(fmt.Sprintf("%q", req.Prompt))
	}
	if req.Agent != "" {
		snapshot.Variables[resumeAgentVar] = json.RawMessage(fmt.Sprintf("%q", req.Agent))
	}
}

func variableJSONString(vars map[string]json.RawMessage, key string) string {
	if vars == nil {
		return ""
	}
	raw, ok := vars[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}
