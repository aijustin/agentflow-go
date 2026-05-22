package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

const examplesRoot = "examples"

func main() {
	kind := flag.String("kind", "scenario", "manifest kind: scenario, tool, skill, or builder")
	flag.Parse()
	switch strings.ToLower(strings.TrimSpace(*kind)) {
	case "tool":
		if flag.NArg() < 1 {
			log.Fatal("usage: validate -kind tool <manifest.yaml>")
		}
		validateTool(flag.Arg(0))
	case "skill":
		if flag.NArg() < 1 {
			log.Fatal("usage: validate -kind skill <manifest.yaml>")
		}
		validateSkill(flag.Arg(0))
	case "builder":
		validateBuilder(flag.Args())
	default:
		if flag.NArg() < 1 {
			log.Fatal("usage: validate [-kind scenario|tool|skill|builder] <manifest.yaml|builder-id|all>")
		}
		validateScenario(flag.Arg(0))
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

func validateBuilder(args []string) {
	target := "all"
	if len(args) > 0 {
		target = strings.TrimSpace(args[0])
	}
	entries := builder.ExampleCatalog()
	if target != "all" {
		entry, ok := builder.FindCatalogEntry(entries, target)
		if !ok {
			log.Fatalf("unknown builder target %q (use catalog id, examples/*.yaml path, or all)", target)
		}
		if err := builder.ValidateCatalogEntry(entry, examplesRoot); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("ok: builder %s (%s)\n", entry.ID, entry.YAML)
		return
	}
	for _, entry := range entries {
		if err := builder.ValidateCatalogEntry(entry, examplesRoot); err != nil {
			log.Fatalf("%s (%s): %v", entry.ID, entry.YAML, err)
		}
	}
	fmt.Printf("ok: builder catalog (%d entries)\n", len(entries))
}
