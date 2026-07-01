package agentflow_test

import (
	"context"
	"errors"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
)

func TestFrameworkClose(t *testing.T) {
	closed := false
	fw, err := agentflow.New(testAutonomousScenario(), agentflow.WithToolExecutor("echo", noopTool{}), agentflow.WithCloser(func(context.Context) error {
		closed = true
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := fw.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("expected closer to run")
	}
}

func TestFrameworkCloseJoinsErrors(t *testing.T) {
	fw, err := agentflow.New(testAutonomousScenario(), agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithCloser(func(context.Context) error { return errors.New("one") }),
		agentflow.WithCloser(func(context.Context) error { return errors.New("two") }),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fw.Close(context.Background()); err == nil {
		t.Fatal("expected close error")
	}
}

func TestFrameworkCloseGivesCloserTimeoutContext(t *testing.T) {
	var hasDeadline bool
	fw, err := agentflow.New(testAutonomousScenario(), agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithCloser(func(ctx context.Context) error {
			_, hasDeadline = ctx.Deadline()
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fw.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !hasDeadline {
		t.Fatal("expected closer to receive a timeout context when caller has no deadline")
	}
}
