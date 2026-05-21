package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

func main() {
	scenarioFile := "../../ticket_handling.yaml"
	scenario, err := agentflow.LoadScenarioFile(scenarioFile)
	if err != nil {
		log.Fatal(err)
	}
	workDir, err := testutil.ScenarioWorkDir(scenarioFile)
	if err != nil {
		log.Fatal(err)
	}
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: workDir})
	if err != nil {
		log.Fatal(err)
	}
	opts = append(opts, agentflow.WithHITLTokenSecret([]byte("dev-secret"), nil))
	fw, err := agentflow.New(scenario, opts...)
	if err != nil {
		log.Fatal(err)
	}
	defer fw.Close(context.Background())

	result, err := fw.HandleEvent(context.Background(), agentflow.IncomingEvent{
		Type: "ticket.created",
		Payload: json.RawMessage(`{
			"body": {
				"ticket_id": "T-9",
				"summary": "Customer cannot reset password"
			}
		}`),
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("run_id=%s status=%s output=%s\n", result.RunID, result.Status, result.Output)
}
