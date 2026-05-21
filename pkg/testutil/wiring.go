package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/internal/adapter/tool/builtin"
	"github.com/aijustin/agentflow-go/pkg/core"
)

// WiringConfig controls test and example wiring for scenarios loaded from YAML.
type WiringConfig struct {
	// WorkDir is the default git allowlist root and relative repo path base.
	WorkDir string
	// GitRoots overrides git allowlist roots; WorkDir is used when empty.
	GitRoots []string
}

// WiringOptions registers a mock LLM gateway and built-in demo tool executors
// declared in a scenario. Use in tests and examples only; production embedders
// should register real gateways and executors explicitly.
func WiringOptions(scenario core.Scenario, config WiringConfig) ([]agentflow.Option, error) {
	workDir := strings.TrimSpace(config.WorkDir)
	if workDir == "" {
		workDir = "."
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("testutil: resolve work dir: %w", err)
	}
	opts := []agentflow.Option{agentflow.WithLLMGateway(agentflow.NewMockLLMGateway(scenario))}
	for name, tool := range scenario.Tools {
		executor, err := demoToolExecutor(name, tool, absWorkDir, config.GitRoots)
		if err != nil {
			return nil, err
		}
		if executor == nil {
			continue
		}
		opts = append(opts, agentflow.WithToolExecutor(name, executor))
	}
	return opts, nil
}

// ScenarioWorkDir returns the directory containing a scenario file, or the
// current working directory when the path is empty.
func ScenarioWorkDir(scenarioFile string) (string, error) {
	if strings.TrimSpace(scenarioFile) == "" {
		return os.Getwd()
	}
	return filepath.Abs(filepath.Dir(scenarioFile))
}

func demoToolExecutor(name string, tool core.Tool, workDir string, gitRoots []string) (core.ToolExecutor, error) {
	switch strings.TrimSpace(tool.Type) {
	case "builtin.echo", "builtin.repo_search":
		return builtin.NewEchoTool(), nil
	case "builtin.git":
		roots := demoGitRoots(workDir, gitRoots)
		return agentflow.NewGitToolExecutor(agentflow.GitToolConfig{AllowedRoots: roots})
	case "builtin.ticket":
		return agentflow.NewTicketToolExecutor(agentflow.TicketToolConfig{Store: agentflow.NewMemoryTicketStore(nil)})
	case "":
		return nil, fmt.Errorf("testutil: tool %q is missing type", name)
	default:
		return nil, nil
	}
}

func demoGitRoots(workDir string, configured []string) []string {
	if len(configured) > 0 {
		return configured
	}
	roots := []string{workDir}
	if root, err := findGitRoot(workDir); err == nil {
		roots = appendUniquePath(roots, root)
	}
	if cwd, err := os.Getwd(); err == nil {
		roots = appendUniquePath(roots, cwd)
		if root, err := findGitRoot(cwd); err == nil {
			roots = appendUniquePath(roots, root)
		}
	}
	return roots
}

func findGitRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("testutil: git root not found from %s", start)
		}
		dir = parent
	}
}

func appendUniquePath(paths []string, candidate string) []string {
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return paths
	}
	for _, existing := range paths {
		if existing == abs {
			return paths
		}
	}
	return append(paths, abs)
}
