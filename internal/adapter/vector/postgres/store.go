package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/knowledge"
)

const DefaultTableName = "agentflow_knowledge_embeddings"

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Option func(*Store) error

type Store struct {
	db    *sql.DB
	table string
}

func NewStore(db *sql.DB, opts ...Option) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("postgres vector store: db is nil")
	}
	store := &Store{db: db, table: DefaultTableName}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(store); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func WithTableName(name string) Option {
	return func(store *Store) error {
		if !validTableName(name) {
			return fmt.Errorf("postgres vector store: invalid table name %q", name)
		}
		store.table = name
		return nil
	}
}

func (s *Store) Upsert(ctx context.Context, documents []knowledge.DocumentEmbedding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	query := fmt.Sprintf(`INSERT INTO %s (namespace, document_id, content, metadata_json, embedding)
VALUES ($1, $2, $3, $4, $5::vector)
ON CONFLICT (namespace, document_id) DO UPDATE SET
	content = EXCLUDED.content,
	metadata_json = EXCLUDED.metadata_json,
	embedding = EXCLUDED.embedding`, s.table)
	for _, document := range documents {
		if strings.TrimSpace(document.Document.ID) == "" {
			return fmt.Errorf("postgres vector store: document id is required")
		}
		vector, err := vectorLiteral(document.Vector)
		if err != nil {
			return err
		}
		metadata, err := json.Marshal(document.Document.Metadata)
		if err != nil {
			return fmt.Errorf("postgres vector store: marshal metadata for %q: %w", document.Document.ID, err)
		}
		if string(metadata) == "null" {
			metadata = []byte(`{}`)
		}
		if _, err := s.db.ExecContext(ctx, query, document.Document.Namespace, document.Document.ID, document.Document.Content, metadata, vector); err != nil {
			return fmt.Errorf("postgres vector store: upsert %q: %w", document.Document.ID, err)
		}
	}
	return nil
}

func (s *Store) Query(ctx context.Context, query knowledge.Query) ([]knowledge.SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vector, err := vectorLiteral(query.Vector)
	if err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 5
	}
	args := []any{query.Namespace, vector}
	where := "WHERE namespace = $1"
	if len(query.Filter) > 0 {
		metadataFilter, err := json.Marshal(query.Filter)
		if err != nil {
			return nil, fmt.Errorf("postgres vector store: marshal metadata filter: %w", err)
		}
		args = append(args, metadataFilter)
		where += fmt.Sprintf(" AND metadata_json @> $%d::jsonb", len(args))
	}
	args = append(args, limit)
	statement := fmt.Sprintf(`SELECT document_id, content, metadata_json, 1 - (embedding <=> $2::vector) AS score
FROM %s
%s
ORDER BY embedding <=> $2::vector
LIMIT $%d`, s.table, where, len(args))
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres vector store: query: %w", err)
	}
	defer rows.Close()
	results := make([]knowledge.SearchResult, 0)
	for rows.Next() {
		var id string
		var content string
		var metadataRaw []byte
		var score float64
		if err := rows.Scan(&id, &content, &metadataRaw, &score); err != nil {
			return nil, fmt.Errorf("postgres vector store: scan result: %w", err)
		}
		metadata := make(map[string]string)
		if len(metadataRaw) > 0 && string(metadataRaw) != "null" {
			if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
				return nil, fmt.Errorf("postgres vector store: decode metadata for %q: %w", id, err)
			}
		}
		results = append(results, knowledge.SearchResult{Document: knowledge.Document{ID: id, Namespace: query.Namespace, Content: content, Metadata: metadata}, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres vector store: query rows: %w", err)
	}
	return results, nil
}

func (s *Store) Delete(ctx context.Context, req knowledge.DeleteRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(req.ID) == "" {
		return fmt.Errorf("postgres vector store: document id is required")
	}
	query := fmt.Sprintf(`DELETE FROM %s WHERE namespace = $1 AND document_id = $2`, s.table)
	if _, err := s.db.ExecContext(ctx, query, req.Namespace, req.ID); err != nil {
		return fmt.Errorf("postgres vector store: delete %q: %w", req.ID, err)
	}
	return nil
}

func vectorLiteral(vector []float32) (string, error) {
	if len(vector) == 0 {
		return "", fmt.Errorf("postgres vector store: vector is required")
	}
	var builder strings.Builder
	builder.WriteByte('[')
	for index, value := range vector {
		floatValue := float64(value)
		if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
			return "", fmt.Errorf("postgres vector store: vector contains non-finite value at index %d", index)
		}
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(strconv.FormatFloat(floatValue, 'g', -1, 32))
	}
	builder.WriteByte(']')
	return builder.String(), nil
}

func validTableName(name string) bool {
	if name == "" {
		return false
	}
	parts := strings.Split(name, ".")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if !identifierPattern.MatchString(part) {
			return false
		}
	}
	return true
}
