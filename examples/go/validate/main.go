package main

import (
	"fmt"
	"log"
	"os"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: validate <scenario.yaml>")
	}
	path := os.Args[1]
	scenario, err := agentflow.LoadScenarioFile(path)
	if err != nil {
		log.Fatal(err)
	}
	workDir, err := testutil.ScenarioWorkDir(path)
	if err != nil {
		log.Fatal(err)
	}
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: workDir})
	if err != nil {
		log.Fatal(err)
	}
	if err := agentflow.ValidateWiring(scenario, opts...); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("ok: %s\n", path)
}
