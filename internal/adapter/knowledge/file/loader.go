package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/knowledge"
)

type Config struct {
	Paths     []string
	Namespace string
	Metadata  map[string]string
	MaxBytes  int64
}

type Loader struct {
	paths     []string
	namespace string
	metadata  map[string]string
	maxBytes  int64
}

func NewLoader(config Config) (*Loader, error) {
	if len(config.Paths) == 0 {
		return nil, fmt.Errorf("file knowledge loader: at least one path is required")
	}
	paths := make([]string, 0, len(config.Paths))
	for _, path := range config.Paths {
		path = strings.TrimSpace(path)
		if path == "" {
			return nil, fmt.Errorf("file knowledge loader: path is required")
		}
		paths = append(paths, path)
	}
	return &Loader{paths: paths, namespace: config.Namespace, metadata: cloneMetadata(config.Metadata), maxBytes: config.MaxBytes}, nil
}

func (l *Loader) Load(ctx context.Context) ([]knowledge.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := make([]loadItem, 0)
	for _, path := range l.paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("file knowledge loader: stat %q: %w", path, err)
		}
		if info.IsDir() {
			root := path
			if err := filepath.WalkDir(root, func(itemPath string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if !info.Mode().IsRegular() {
					return nil
				}
				rel, err := filepath.Rel(root, itemPath)
				if err != nil {
					return err
				}
				items = append(items, loadItem{path: itemPath, id: filepath.ToSlash(rel), size: info.Size()})
				return nil
			}); err != nil {
				return nil, fmt.Errorf("file knowledge loader: walk %q: %w", path, err)
			}
			continue
		}
		if info.Mode().IsRegular() {
			items = append(items, loadItem{path: path, id: filepath.ToSlash(filepath.Base(path)), size: info.Size()})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })
	documents := make([]knowledge.Document, 0, len(items))
	for _, item := range items {
		if l.maxBytes > 0 && item.size > l.maxBytes {
			return nil, fmt.Errorf("file knowledge loader: %q exceeds max bytes", item.path)
		}
		content, err := os.ReadFile(item.path)
		if err != nil {
			return nil, fmt.Errorf("file knowledge loader: read %q: %w", item.path, err)
		}
		metadata := cloneMetadata(l.metadata)
		metadata["path"] = item.path
		metadata["extension"] = filepath.Ext(item.path)
		documents = append(documents, knowledge.Document{ID: item.id, Content: string(content), Metadata: metadata, Namespace: l.namespace})
	}
	return documents, nil
}

type loadItem struct {
	path string
	id   string
	size int64
}

func cloneMetadata(metadata map[string]string) map[string]string {
	out := make(map[string]string, len(metadata)+2)
	for key, value := range metadata {
		out[key] = value
	}
	return out
}
