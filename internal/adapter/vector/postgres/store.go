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

// maxUpsertRows caps the number of rows per multi-row INSERT so the total
// number of bind parameters (upsertColumns per row) stays well under the
// PostgreSQL limit of 65535.
const maxUpsertRows = 500

const upsertColumns = 5

type upsertRow struct {
	namespace string
	id        string
	content   string
	metadata  []byte
	vector    string
}

func (s *Store) Upsert(ctx context.Context, documents []knowledge.DocumentEmbedding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(documents) == 0 {
		return nil
	}

	// Validate and encode every document before touching the database so the
	// whole batch fails atomically on bad input. De-duplicate on
	// (namespace, document_id) keeping the last occurrence, because a single
	// multi-row INSERT ... ON CONFLICT cannot affect the same row twice.
	rows := make([]upsertRow, 0, len(documents))
	indexByKey := make(map[string]int, len(documents))
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
		row := upsertRow{
			namespace: document.Document.Namespace,
			id:        document.Document.ID,
			content:   document.Document.Content,
			metadata:  metadata,
			vector:    vector,
		}
		key := row.namespace + "\x00" + row.id
		if idx, ok := indexByKey[key]; ok {
			rows[idx] = row
			continue
		}
		indexByKey[key] = len(rows)
		rows = append(rows, row)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres vector store: begin upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for start := 0; start < len(rows); start += maxUpsertRows {
		end := start + maxUpsertRows
		if end > len(rows) {
			end = len(rows)
		}
		query, args := s.buildUpsertQuery(rows[start:end])
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("postgres vector store: upsert batch: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres vector store: commit upsert: %w", err)
	}
	return nil
}

func (s *Store) buildUpsertQuery(rows []upsertRow) (string, []any) {
	var builder strings.Builder
	builder.WriteString("INSERT INTO ")
	builder.WriteString(s.table)
	builder.WriteString(" (namespace, document_id, content, metadata_json, embedding)\nVALUES ")
	args := make([]any, 0, len(rows)*upsertColumns)
	for i, row := range rows {
		if i > 0 {
			builder.WriteString(", ")
		}
		base := i * upsertColumns
		fmt.Fprintf(&builder, "($%d, $%d, $%d, $%d, $%d::vector)", base+1, base+2, base+3, base+4, base+5)
		args = append(args, row.namespace, row.id, row.content, row.metadata, row.vector)
	}
	builder.WriteString(`
ON CONFLICT (namespace, document_id) DO UPDATE SET
	content = EXCLUDED.content,
	metadata_json = EXCLUDED.metadata_json,
	embedding = EXCLUDED.embedding`)
	return builder.String(), args
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

// HybridQuery combines vector and full-text search with reciprocal rank fusion.
func (s *Store) HybridQuery(ctx context.Context, query knowledge.Query) ([]knowledge.SearchResult, error) {
	vectorResults, err := s.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	textResults, err := s.textSearch(ctx, query)
	if err != nil {
		return vectorResults, nil
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 5
	}
	return knowledge.MergeRRF([][]knowledge.SearchResult{vectorResults, textResults}, 60, limit), nil
}

func (s *Store) textSearch(ctx context.Context, query knowledge.Query) ([]knowledge.SearchResult, error) {
	if strings.TrimSpace(query.Text) == "" {
		return nil, nil
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 5
	}
	args := []any{query.Namespace, query.Text}
	where := "WHERE namespace = $1 AND to_tsvector('simple', content) @@ plainto_tsquery('simple', $2)"
	if len(query.Filter) > 0 {
		metadataFilter, err := json.Marshal(query.Filter)
		if err != nil {
			return nil, fmt.Errorf("postgres vector store: marshal metadata filter: %w", err)
		}
		args = append(args, metadataFilter)
		where += fmt.Sprintf(" AND metadata_json @> $%d::jsonb", len(args))
	}
	args = append(args, limit)
	statement := fmt.Sprintf(`SELECT document_id, content, metadata_json, ts_rank(to_tsvector('simple', content), plainto_tsquery('simple', $2)) AS score
FROM %s
%s
ORDER BY score DESC
LIMIT $%d`, s.table, where, len(args))
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres vector store: text search: %w", err)
	}
	defer rows.Close()
	results := make([]knowledge.SearchResult, 0)
	for rows.Next() {
		var id, content string
		var metadataRaw []byte
		var score float64
		if err := rows.Scan(&id, &content, &metadataRaw, &score); err != nil {
			return nil, fmt.Errorf("postgres vector store: scan text result: %w", err)
		}
		metadata := make(map[string]string)
		if len(metadataRaw) > 0 && string(metadataRaw) != "null" {
			if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
				return nil, fmt.Errorf("postgres vector store: decode metadata for %q: %w", id, err)
			}
		}
		results = append(results, knowledge.SearchResult{Document: knowledge.Document{ID: id, Namespace: query.Namespace, Content: content, Metadata: metadata}, Score: score})
	}
	return results, rows.Err()
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
