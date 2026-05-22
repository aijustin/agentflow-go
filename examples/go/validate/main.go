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

func main() {
	kind := flag.String("kind", "builder", "manifest kind: builder, scenario (deprecated), tool, or skill")
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
	case "scenario":
		if flag.NArg() < 1 {
			log.Fatal("usage: validate -kind scenario <legacy.yaml>")
		}
		validateScenarioLegacy(flag.Arg(0))
	default:
		log.Fatal("usage: validate [-kind builder|scenario|tool|skill] [builder-id|all|legacy.yaml]")
	}
}

func validateScenarioLegacy(path string) {
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
	fmt.Printf("ok: legacy scenario %s\n", path)
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
			log.Fatalf("unknown builder target %q (use catalog id or all)", target)
		}
		if err := builder.ValidateCatalogEntry(entry); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("ok: builder %s\n", entry.ID)
		return
	}
	for _, entry := range entries {
		if err := builder.ValidateCatalogEntry(entry); err != nil {
			log.Fatalf("%s: %v", entry.ID, err)
		}
	}
	fmt.Printf("ok: builder catalog (%d entries)\n", len(entries))
}
