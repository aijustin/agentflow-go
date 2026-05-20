package git

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type Config struct {
	AllowedRoots []string
}

type Executor struct {
	allowedRoots []string
}

type Request struct {
	Action string `json:"action"`
	Repo   string `json:"repo"`
	Ref    string `json:"ref,omitempty"`
	Path   string `json:"path,omitempty"`
}

type Response struct {
	Action  string `json:"action"`
	Repo    string `json:"repo"`
	Output  string `json:"output"`
	ExitErr string `json:"exit_error,omitempty"`
}

func NewExecutor(config Config) (*Executor, error) {
	roots, err := normalizeAllowedRoots(config.AllowedRoots)
	if err != nil {
		return nil, err
	}
	return &Executor{allowedRoots: roots}, nil
}

func (e *Executor) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolResult{}, err
	}
	var req Request
	if err := json.Unmarshal(call.Input, &req); err != nil {
		return core.ToolResult{}, fmt.Errorf("git tool: decode input: %w", err)
	}
	repoRoot, err := e.resolveRepo(req.Repo)
	if err != nil {
		return core.ToolResult{}, err
	}
	args, err := e.buildArgs(req)
	if err != nil {
		return core.ToolResult{}, err
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	response := Response{
		Action: req.Action,
		Repo:   repoRoot,
		Output: strings.TrimSpace(stdout.String()),
	}
	if runErr != nil {
		response.ExitErr = strings.TrimSpace(stderr.String())
		if response.ExitErr == "" {
			response.ExitErr = runErr.Error()
		}
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return core.ToolResult{}, err
	}
	if runErr != nil {
		return core.ToolResult{Tool: call.Tool, Output: raw, Error: response.ExitErr}, nil
	}
	return core.ToolResult{Tool: call.Tool, Output: raw}, nil
}

func (e *Executor) buildArgs(req Request) ([]string, error) {
	switch strings.TrimSpace(strings.ToLower(req.Action)) {
	case "diff":
		args := []string{"diff"}
		if ref := strings.TrimSpace(req.Ref); ref != "" {
			args = append(args, ref)
		}
		if path := strings.TrimSpace(req.Path); path != "" {
			args = append(args, "--", path)
		}
		return args, nil
	case "show":
		if strings.TrimSpace(req.Ref) == "" {
			return nil, fmt.Errorf("git tool: show requires ref")
		}
		return []string{"show", req.Ref}, nil
	case "log":
		args := []string{"log", "--oneline", "-n", "20"}
		if ref := strings.TrimSpace(req.Ref); ref != "" {
			args = append(args, ref)
		}
		return args, nil
	case "status":
		return []string{"status", "--short"}, nil
	default:
		return nil, fmt.Errorf("git tool: unsupported action %q", req.Action)
	}
}

func normalizeAllowedRoots(roots []string) ([]string, error) {
	if len(roots) == 0 {
		return nil, fmt.Errorf("git tool: at least one allowed root is required")
	}
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		clean := filepath.Clean(strings.TrimSpace(root))
		if clean == "" || clean == "." {
			return nil, fmt.Errorf("git tool: invalid allowed root %q", root)
		}
		abs, err := filepath.Abs(clean)
		if err != nil {
			return nil, fmt.Errorf("git tool: resolve allowed root %q: %w", root, err)
		}
		out = append(out, abs)
	}
	return out, nil
}

func (e *Executor) resolveRepo(repo string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(repo))
	if clean == "" {
		return "", fmt.Errorf("git tool: repo path is required")
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return "", fmt.Errorf("git tool: resolve repo %q: %w", repo, err)
	}
	for _, root := range e.allowedRoots {
		if abs == root || strings.HasPrefix(abs, root+string(os.PathSeparator)) {
			if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
				return "", fmt.Errorf("git tool: %q is not a git repository", abs)
			}
			return abs, nil
		}
	}
	return "", fmt.Errorf("git tool: repo %q is outside allowed roots", repo)
}
