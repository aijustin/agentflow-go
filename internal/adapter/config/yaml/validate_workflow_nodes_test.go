package yaml

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadWorkflowWithMapParallelAndLoopNodes(t *testing.T) {
	scenario, err := Load([]byte(`
scenario:
  name: advanced-wf
  llms:
    default:
      provider: mock
      model: test
  tools:
    echo:
      type: builtin.echo
      approval: never
  agents:
    worker:
      llm: default
      tools: [echo]
  triggers:
    - event: ticket.created
      agent: worker
  orchestration:
    mode: fixed_workflow
    workflow:
      nodes:
        - id: prep
          kind: transform
          input:
            set:
              items: [1, 2]
        - id: map1
          kind: map
          depends_on: [prep]
          input:
            items_path: items
            branch:
              kind: tool
              ref: echo
            on_error: collect_errors
        - id: pg1
          kind: parallel_group
          input:
            refs: [worker]
            tools: [echo]
        - id: loop1
          kind: loop
          input:
            body: [prep]
      edges: []
`))
	if err != nil {
		t.Fatal(err)
	}
	if scenario.Name != "advanced-wf" {
		t.Fatalf("unexpected scenario: %+v", scenario)
	}
	if len(scenario.Triggers) != 1 || scenario.Triggers[0].Event != "ticket.created" {
		t.Fatalf("unexpected triggers: %+v", scenario.Triggers)
	}
}

func TestLoadFileReadsScenarioFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	content := `
scenario:
  name: from-file
  llms:
    default:
      provider: mock
      model: test
  agents:
    worker:
      llm: default
  orchestration:
    mode: autonomous
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	scenario, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if scenario.Name != "from-file" {
		t.Fatalf("unexpected name %q", scenario.Name)
	}
}

func TestValidateRejectsDuplicateTriggerEvents(t *testing.T) {
	_, err := Load([]byte(`
scenario:
  name: dup-triggers
  llms:
    default:
      provider: mock
      model: test
  agents:
    worker:
      llm: default
  triggers:
    - event: same.event
      agent: worker
    - event: same.event
      agent: worker
  orchestration:
    mode: autonomous
`))
	if err == nil || !strings.Contains(err.Error(), "duplicate trigger") {
		t.Fatalf("expected duplicate trigger error, got %v", err)
	}
}

func TestValidateRejectsInvalidMapNode(t *testing.T) {
	_, err := Load([]byte(`
scenario:
  name: bad-map
  llms:
    default:
      provider: mock
      model: test
  agents:
    worker:
      llm: default
  orchestration:
    mode: fixed_workflow
    workflow:
      nodes:
        - id: map1
          kind: map
          input:
            branch:
              kind: agent
              ref: worker
      edges: []
`))
	if err == nil || !strings.Contains(err.Error(), "items_path") {
		t.Fatalf("expected map items_path error, got %v", err)
	}
}

func TestValidateRejectsParallelGroupDuplicateRefs(t *testing.T) {
	_, err := Load([]byte(`
scenario:
  name: bad-parallel
  llms:
    default:
      provider: mock
      model: test
  agents:
    worker:
      llm: default
  orchestration:
    mode: fixed_workflow
    workflow:
      nodes:
        - id: pg1
          kind: parallel_group
          input:
            refs: [worker, worker]
      edges: []
`))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate refs error, got %v", err)
	}
}
