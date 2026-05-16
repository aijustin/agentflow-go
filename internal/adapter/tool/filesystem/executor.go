package filesystem

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
)

const DefaultMaxBytes int64 = 1 << 20

type Config struct {
	AllowedRoots []string
	MaxBytes     int64
}

type Executor struct {
	allowedRoots []string
	maxBytes     int64
}

type Request struct {
	Path string `json:"path"`
}

type Response struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Content string `json:"content"`
}

type candidate struct {
	root         string
	relativePath string
	outputPath   string
}

func NewExecutor(config Config) (*Executor, error) {
	allowedRoots, err := normalizeAllowedRoots(config.AllowedRoots)
	if err != nil {
		return nil, err
	}
	maxBytes := config.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Executor{allowedRoots: allowedRoots, maxBytes: maxBytes}, nil
}

func (executor *Executor) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolResult{}, err
	}
	if len(call.Input) == 0 {
		return core.ToolResult{}, fmt.Errorf("filesystem tool: input is required")
	}
	var input Request
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return core.ToolResult{}, fmt.Errorf("filesystem tool: decode input: %w", err)
	}
	candidates, err := executor.resolveCandidates(input.Path)
	if err != nil {
		return core.ToolResult{}, err
	}
	var lastErr error
	for _, candidate := range candidates {
		response, err := executor.readCandidate(ctx, candidate)
		if err == nil {
			output, err := json.Marshal(response)
			if err != nil {
				return core.ToolResult{}, err
			}
			return core.ToolResult{Tool: call.Tool, Output: output}, nil
		}
		lastErr = err
		if !os.IsNotExist(err) {
			break
		}
	}
	if lastErr != nil {
		return core.ToolResult{}, fmt.Errorf("filesystem tool: read %q: %w", strings.TrimSpace(input.Path), lastErr)
	}
	return core.ToolResult{}, fmt.Errorf("filesystem tool: path %q is not under allowed roots", strings.TrimSpace(input.Path))
}

func (executor *Executor) readCandidate(ctx context.Context, candidate candidate) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	root, err := os.OpenRoot(candidate.root)
	if err != nil {
		return Response{}, err
	}
	defer root.Close()
	file, err := root.Open(candidate.relativePath)
	if err != nil {
		return Response{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Response{}, err
	}
	if info.IsDir() {
		return Response{}, fmt.Errorf("path is a directory")
	}
	content, err := readLimited(file, executor.maxBytes)
	if err != nil {
		return Response{}, err
	}
	return Response{Path: candidate.outputPath, Size: int64(len(content)), Content: string(content)}, nil
}

func (executor *Executor) resolveCandidates(rawPath string) ([]candidate, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return nil, fmt.Errorf("filesystem tool: path is required")
	}
	if filepath.IsAbs(rawPath) {
		absolutePath := filepath.Clean(rawPath)
		comparePath := absolutePath
		if resolvedPath, err := filepath.EvalSymlinks(absolutePath); err == nil {
			comparePath = filepath.Clean(resolvedPath)
		}
		for _, root := range executor.allowedRoots {
			if !withinRoot(comparePath, root) {
				continue
			}
			relativePath, err := filepath.Rel(root, comparePath)
			if err != nil {
				return nil, fmt.Errorf("filesystem tool: resolve path: %w", err)
			}
			return []candidate{{root: root, relativePath: relativePath, outputPath: absolutePath}}, nil
		}
		return nil, fmt.Errorf("filesystem tool: path %q is not under allowed roots", rawPath)
	}
	cleanPath := filepath.Clean(rawPath)
	if cleanPath == "." {
		return nil, fmt.Errorf("filesystem tool: path is required")
	}
	candidates := make([]candidate, 0, len(executor.allowedRoots))
	for _, root := range executor.allowedRoots {
		candidates = append(candidates, candidate{root: root, relativePath: cleanPath, outputPath: filepath.Join(root, cleanPath)})
	}
	return candidates, nil
}

func normalizeAllowedRoots(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("filesystem tool: at least one allowed root is required")
	}
	seen := map[string]bool{}
	roots := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("filesystem tool: allowed root is required")
		}
		absolutePath, err := filepath.Abs(value)
		if err != nil {
			return nil, fmt.Errorf("filesystem tool: resolve allowed root %q: %w", value, err)
		}
		resolvedPath, err := filepath.EvalSymlinks(absolutePath)
		if err != nil {
			return nil, fmt.Errorf("filesystem tool: resolve allowed root %q: %w", value, err)
		}
		info, err := os.Stat(resolvedPath)
		if err != nil {
			return nil, fmt.Errorf("filesystem tool: stat allowed root %q: %w", value, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("filesystem tool: allowed root %q is not a directory", value)
		}
		resolvedPath = filepath.Clean(resolvedPath)
		if !seen[resolvedPath] {
			seen[resolvedPath] = true
			roots = append(roots, resolvedPath)
		}
	}
	return roots, nil
}

func withinRoot(path string, root string) bool {
	relativePath, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relativePath == "." || (relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)))
}

func readLimited(reader io.Reader, maxBytes int64) ([]byte, error) {
	var buffer bytes.Buffer
	limited := io.LimitReader(reader, maxBytes+1)
	if _, err := buffer.ReadFrom(limited); err != nil {
		return nil, err
	}
	if int64(buffer.Len()) > maxBytes {
		return nil, fmt.Errorf("file exceeds max bytes")
	}
	return buffer.Bytes(), nil
}
