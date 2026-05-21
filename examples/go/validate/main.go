package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

func main() {
	kind := flag.String("kind", "scenario", "manifest kind: scenario, tool, or skill")
	flag.Parse()
	if flag.NArg() < 1 {
		log.Fatal("usage: validate [-kind scenario|tool|skill] <manifest.yaml>")
	}
	path := flag.Arg(0)
	switch strings.ToLower(strings.TrimSpace(*kind)) {
	case "tool":
		validateTool(path)
	case "skill":
		validateSkill(path)
	default:
		validateScenario(path)
	}
}

func validateScenario(path string) {
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
	fmt.Printf("ok: scenario %s\n", path)
}

func validateTool(path string) {
	tool, err := agentflow.LoadToolManifestFile(path)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("ok: tool %s (%s)\n", tool.Name, path)
}

func validateSkill(path string) {
	skill, err := agentflow.LoadSkillManifestFile(path)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("ok: skill %s (%s)\n", skill.Name, path)
}
