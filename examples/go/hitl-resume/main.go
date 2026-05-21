package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

func main() {
	scenarioFile := "../../human_in_loop.yaml"
	scenario, err := agentflow.LoadScenarioFile(scenarioFile)
	if err != nil {
		log.Fatal(err)
	}
	workDir, err := testutil.ScenarioWorkDir(scenarioFile)
	if err != nil {
		log.Fatal(err)
	}

	var tokenBuf bytes.Buffer
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: workDir})
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
	_, err = fw.Run(ctx, agentflow.RunRequest{
		RunID:  "hitl-run-1",
		Agent:  "assistant",
		Prompt: "draft a response for approval",
	})
	if err == nil {
		log.Fatal("expected run to pause for human approval")
	}
	if !strings.Contains(err.Error(), "human") && !strings.Contains(err.Error(), "gate") {
		log.Fatalf("unexpected pause error: %v", err)
	}

	token := strings.TrimSpace(tokenBuf.String())
	if token == "" {
		log.Fatal("expected HITL token on stderr")
	}

	result, err := fw.ResumeAndContinue(ctx, token, core.DecisionApprove, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Output)
}
