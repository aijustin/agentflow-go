package toolinvoke

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

type stubEvaluator struct {
	pause bool
	err   error
}

func (s stubEvaluator) PauseRequired(context.Context, string, core.Tool, llm.ToolCall) (bool, error) {
	return s.pause, s.err
}

func TestEvaluatePauseRequiredStaticPolicy(t *testing.T) {
	tool := core.Tool{Approval: core.ApprovalPause}
	call := llm.ToolCall{Name: "write"}
	pause, err := EvaluatePauseRequired(context.Background(), tool, stubEvaluator{pause: false}, "run-1", call)
	if err != nil || !pause {
		t.Fatalf("pause=%v err=%v", pause, err)
	}
}

func TestEvaluatePauseRequiredDynamicEvaluator(t *testing.T) {
	tool := core.Tool{Approval: core.ApprovalNever}
	call := llm.ToolCall{Name: "invoke_proxy"}
	pause, err := EvaluatePauseRequired(context.Background(), tool, stubEvaluator{pause: true}, "run-1", call)
	if err != nil || !pause {
		t.Fatalf("pause=%v err=%v", pause, err)
	}
	pause, err = EvaluatePauseRequired(context.Background(), tool, nil, "run-1", call)
	if err != nil || pause {
		t.Fatalf("without evaluator pause=%v err=%v", pause, err)
	}
}
