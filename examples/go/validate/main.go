package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/builder"
)

func main() {
	kind := flag.String("kind", "builder", "manifest kind: builder, tool, or skill")
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
		log.Fatal("usage: validate [-kind builder|tool|skill] [builder-id|core|full|all|manifest.yaml]")
	}
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
	// Default CI surface is CoreCatalog (autonomous). Use "full"/"all" for ExampleCatalog.
	target := "core"
	if len(args) > 0 {
		target = strings.TrimSpace(args[0])
	}
	switch strings.ToLower(target) {
	case "core", "":
		validateCatalogEntries(builder.CoreCatalog(), "core")
	case "full", "all":
		validateCatalogEntries(builder.ExampleCatalog(), "full")
	case "legacy":
		validateCatalogEntries(builder.LegacyCatalog(), "legacy")
	default:
		entries := builder.ExampleCatalog()
		entry, ok := builder.FindCatalogEntry(entries, target)
		if !ok {
			log.Fatalf("unknown builder target %q (use catalog id, core, legacy, or full)", target)
		}
		if err := builder.ValidateCatalogEntry(entry); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("ok: builder %s\n", entry.ID)
	}
}

func validateCatalogEntries(entries []builder.CatalogEntry, label string) {
	for _, entry := range entries {
		if err := builder.ValidateCatalogEntry(entry); err != nil {
			log.Fatalf("%s: %v", entry.ID, err)
		}
	}
	fmt.Printf("ok: builder %s catalog (%d entries)\n", label, len(entries))
}
