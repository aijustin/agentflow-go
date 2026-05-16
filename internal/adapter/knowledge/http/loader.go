package http

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/knowledge"
)

type Config struct {
	URLs      []string
	Namespace string
	Metadata  map[string]string
	MaxBytes  int64
	Client    *nethttp.Client
}

type Loader struct {
	urls      []string
	namespace string
	metadata  map[string]string
	maxBytes  int64
	client    *nethttp.Client
}

func NewLoader(config Config) (*Loader, error) {
	if len(config.URLs) == 0 {
		return nil, fmt.Errorf("http knowledge loader: at least one URL is required")
	}
	urls := make([]string, 0, len(config.URLs))
	for _, rawURL := range config.URLs {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			return nil, fmt.Errorf("http knowledge loader: URL is required")
		}
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("http knowledge loader: invalid URL %q", rawURL)
		}
		switch parsed.Scheme {
		case "http", "https":
		default:
			return nil, fmt.Errorf("http knowledge loader: unsupported URL scheme %q", parsed.Scheme)
		}
		urls = append(urls, rawURL)
	}
	client := config.Client
	if client == nil {
		client = nethttp.DefaultClient
	}
	return &Loader{urls: urls, namespace: config.Namespace, metadata: cloneMetadata(config.Metadata), maxBytes: config.MaxBytes, client: client}, nil
}

func (l *Loader) Load(ctx context.Context) ([]knowledge.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	documents := make([]knowledge.Document, 0, len(l.urls))
	for _, rawURL := range l.urls {
		document, err := l.loadURL(ctx, rawURL)
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, nil
}

func (l *Loader) loadURL(ctx context.Context, rawURL string) (knowledge.Document, error) {
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, rawURL, nil)
	if err != nil {
		return knowledge.Document{}, fmt.Errorf("http knowledge loader: build request %q: %w", rawURL, err)
	}
	req.Header.Set("Accept", "text/markdown,text/plain,text/html,application/json;q=0.8,*/*;q=0.1")
	resp, err := l.client.Do(req)
	if err != nil {
		return knowledge.Document{}, fmt.Errorf("http knowledge loader: fetch %q: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return knowledge.Document{}, fmt.Errorf("http knowledge loader: fetch %q: unexpected status %s", rawURL, resp.Status)
	}
	content, err := readLimited(resp.Body, l.maxBytes)
	if err != nil {
		return knowledge.Document{}, fmt.Errorf("http knowledge loader: read %q: %w", rawURL, err)
	}
	metadata := cloneMetadata(l.metadata)
	metadata["url"] = rawURL
	metadata["source"] = firstNonEmpty(metadata["source"], rawURL)
	metadata["status_code"] = strconv.Itoa(resp.StatusCode)
	if contentType := strings.TrimSpace(resp.Header.Get("Content-Type")); contentType != "" {
		metadata["content_type"] = strings.Split(contentType, ";")[0]
	}
	return knowledge.Document{ID: rawURL, Content: string(content), Metadata: metadata, Namespace: l.namespace}, nil
}

func readLimited(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return io.ReadAll(reader)
	}
	content, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxBytes {
		return nil, fmt.Errorf("response exceeds max bytes")
	}
	return content, nil
}

func cloneMetadata(metadata map[string]string) map[string]string {
	out := make(map[string]string, len(metadata)+4)
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
