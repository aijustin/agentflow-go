package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"

	examplescenario "github.com/aijustin/agentflow-go/examples/go/scenario"
	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

func main() {
	scenario := examplescenario.HumanInLoop()

	var tokenBuf bytes.Buffer
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: examplescenario.WorkDir})
	if err != nil {
		log.Fatal(err)
	}
	opts = append(opts,
		agentflow.WithHITLTokenSecret([]byte("dev-secret"), &tokenBuf),
	)

	fw, err := agentflow.New(scenario, opts...)
	if err != nil {
		log.Fatal(err)
	}
	defer fw.Close(context.Background())

	ctx := context.Background()
	result, err := fw.Run(ctx, agentflow.RunRequest{
		RunID:  "hitl-run-1",
		Agent:  "assistant",
		Prompt: "draft a response for approval",
	})
	if err != nil {
		log.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		log.Fatalf("expected paused run, got status=%s result=%+v", result.Status, result)
	}

	token := strings.TrimSpace(result.Token)
	if token == "" {
		token = strings.TrimSpace(tokenBuf.String())
	}
	if token == "" {
		log.Fatal("expected HITL token")
	}

	resumed, err := fw.ResumeAndContinue(ctx, token, core.DecisionApprove, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(resumed.Output)
}
