package agentflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	"github.com/aijustin/agentflow-go/internal/adapter/tool/builtin"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

// DemoConfig controls local demo wiring for scenarios loaded from YAML.
type DemoConfig struct {
	// WorkDir is used as the default git allowlist root and relative repo path base.
	WorkDir string
	// GitRoots overrides git allowlist roots; WorkDir is used when empty.
	GitRoots []string
}

// DemoOptions registers mock LLM gateways and built-in demo tool executors declared
// in a scenario. Production services should register real executors explicitly.
func DemoOptions(scenario core.Scenario, config DemoConfig) ([]Option, error) {
	workDir := strings.TrimSpace(config.WorkDir)
	if workDir == "" {
		workDir = "."
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("agentflow: resolve demo work dir: %w", err)
	}
	opts := []Option{WithLLMGateway(demoLLMGateway(scenario))}
	for name, tool := range scenario.Tools {
		executor, err := demoToolExecutor(name, tool, absWorkDir, config.GitRoots)
		if err != nil {
			return nil, err
		}
		if executor == nil {
			continue
		}
		opts = append(opts, WithToolExecutor(name, executor))
	}
	return opts, nil
}

func demoLLMGateway(scenario core.Scenario) llm.Gateway {
	gateway := mock.NewFallbackGateway()
	for name, ref := range scenario.LLMs {
		if ref.Provider != "mock" {
			continue
		}
		gateway.SetCapabilities(name, llm.CapChat, llm.CapToolCall, llm.CapStructuredOutput, llm.CapStream, llm.CapEmbed)
	}
	return gateway
}

func demoToolExecutor(name string, tool core.Tool, workDir string, gitRoots []string) (core.ToolExecutor, error) {
	switch strings.TrimSpace(tool.Type) {
	case "builtin.echo", "builtin.repo_search":
		return builtin.NewEchoTool(), nil
	case "builtin.git":
		roots := demoGitRoots(workDir, gitRoots)
		return NewGitToolExecutor(GitToolConfig{AllowedRoots: roots})
	case "builtin.ticket":
		return NewTicketToolExecutor(TicketToolConfig{Store: NewMemoryTicketStore(nil)})
	case "":
		return nil, fmt.Errorf("agentflow: tool %q is missing type", name)
	default:
		return nil, nil
	}
}

// DemoWorkDir returns the directory containing a scenario file, or the current
// working directory when the path is empty.
func DemoWorkDir(scenarioFile string) (string, error) {
	if strings.TrimSpace(scenarioFile) == "" {
		return os.Getwd()
	}
	return filepath.Abs(filepath.Dir(scenarioFile))
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
			return "", fmt.Errorf("agentflow: git root not found from %s", start)
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
