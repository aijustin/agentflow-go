package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
)

const DefaultMaxResponseBytes int64 = 1 << 20

type Config struct {
	AllowedHosts     []string
	AllowedMethods   []string
	DefaultHeaders   map[string]string
	MaxResponseBytes int64
	Client           *nethttp.Client
}

type Executor struct {
	allowedHosts     map[string]bool
	allowedMethods   map[string]bool
	defaultHeaders   map[string]string
	maxResponseBytes int64
	client           *nethttp.Client
}

type Request struct {
	Method  string            `json:"method,omitempty"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

type Response struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
}

func NewExecutor(config Config) (*Executor, error) {
	allowedHosts, err := normalizeAllowedHosts(config.AllowedHosts)
	if err != nil {
		return nil, err
	}
	allowedMethods := normalizeAllowedMethods(config.AllowedMethods)
	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	baseClient := config.Client
	if baseClient == nil {
		baseClient = nethttp.DefaultClient
	}
	// Copy rather than mutate the caller-supplied client: the allowlist
	// check on the request URL only covers the initial request, and
	// net/http follows redirects by default. Without re-checking the
	// allowlist on every hop, an allowed host can redirect the request to
	// an internal/metadata address (e.g. 169.254.169.254) and this tool
	// would happily fetch and return it, defeating the allowlist (SSRF).
	client := *baseClient
	client.CheckRedirect = func(req *nethttp.Request, via []*nethttp.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("http tool: stopped after 10 redirects")
		}
		if !allowedHosts[req.URL.Host] {
			return fmt.Errorf("http tool: redirect to host %q is not allowed", req.URL.Host)
		}
		return nil
	}
	return &Executor{allowedHosts: allowedHosts, allowedMethods: allowedMethods, defaultHeaders: cloneMap(config.DefaultHeaders), maxResponseBytes: maxResponseBytes, client: &client}, nil
}

func (e *Executor) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolResult{}, err
	}
	var input Request
	if len(call.Input) == 0 {
		return core.ToolResult{}, fmt.Errorf("http tool: input is required")
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return core.ToolResult{}, fmt.Errorf("http tool: decode input: %w", err)
	}
	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if method == "" {
		method = nethttp.MethodGet
	}
	if !e.allowedMethods[method] {
		return core.ToolResult{}, fmt.Errorf("http tool: method %q is not allowed", method)
	}
	parsed, err := parseHTTPURL(input.URL)
	if err != nil {
		return core.ToolResult{}, err
	}
	if !e.allowedHosts[parsed.Host] {
		return core.ToolResult{}, fmt.Errorf("http tool: host %q is not allowed", parsed.Host)
	}
	var body io.Reader
	if input.Body != "" {
		body = strings.NewReader(input.Body)
	}
	req, err := nethttp.NewRequestWithContext(ctx, method, parsed.String(), body)
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("http tool: build request: %w", err)
	}
	for key, value := range e.defaultHeaders {
		req.Header.Set(key, value)
	}
	for key, value := range input.Headers {
		req.Header.Set(key, value)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("http tool: request %q: %w", parsed.String(), err)
	}
	defer resp.Body.Close()
	content, err := readLimited(resp.Body, e.maxResponseBytes)
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("http tool: read response: %w", err)
	}
	output, err := json.Marshal(Response{StatusCode: resp.StatusCode, Headers: responseHeaders(resp.Header), Body: string(content)})
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{Tool: call.Tool, Output: output}, nil
}

func normalizeAllowedHosts(values []string) (map[string]bool, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("http tool: at least one allowed host is required")
	}
	out := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("http tool: allowed host is required")
		}
		if strings.Contains(value, "://") {
			parsed, err := parseHTTPURL(value)
			if err != nil {
				return nil, fmt.Errorf("http tool: invalid allowed host %q: %w", value, err)
			}
			value = parsed.Host
		}
		out[value] = true
	}
	return out, nil
}

func normalizeAllowedMethods(values []string) map[string]bool {
	if len(values) == 0 {
		values = []string{nethttp.MethodGet, nethttp.MethodHead}
	}
	out := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func parseHTTPURL(rawURL string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("http tool: url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("http tool: invalid url %q", rawURL)
	}
	switch parsed.Scheme {
	case "http", "https":
		return parsed, nil
	default:
		return nil, fmt.Errorf("http tool: unsupported url scheme %q", parsed.Scheme)
	}
}

func readLimited(reader io.Reader, maxBytes int64) ([]byte, error) {
	var buffer bytes.Buffer
	limited := io.LimitReader(reader, maxBytes+1)
	if _, err := buffer.ReadFrom(limited); err != nil {
		return nil, err
	}
	if int64(buffer.Len()) > maxBytes {
		return nil, fmt.Errorf("response exceeds max bytes")
	}
	return buffer.Bytes(), nil
}

func responseHeaders(headers nethttp.Header) map[string]string {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		out[key] = strings.Join(headers.Values(key), ",")
	}
	return out
}

func cloneMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
