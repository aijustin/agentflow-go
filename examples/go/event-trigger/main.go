package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	agentflow "github.com/aijustin/agentflow-go"
	examplescenario "github.com/aijustin/agentflow-go/examples/go/scenario"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

func main() {
	scenario := examplescenario.TicketHandling()
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: examplescenario.WorkDir})
	if err != nil {
		log.Fatal(err)
	}
	opts = append(opts, agentflow.WithHITLTokenSecret([]byte("dev-secret-16bytes"), nil))
	fw, err := agentflow.New(scenario, opts...)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = fw.Close(context.Background()) }()

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
