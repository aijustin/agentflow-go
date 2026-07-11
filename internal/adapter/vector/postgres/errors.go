package postgres

import (
	"errors"
	"fmt"
)

// ErrDimensionMismatch is returned when an upsert vector length does not match
// the store's configured expected dimension.
var ErrDimensionMismatch = errors.New("postgres vector store: embedding dimension mismatch")

// ReindexRequiredError indicates stored/incoming embeddings use a different
// dimension than the store expects and the collection must be reindexed.
type ReindexRequiredError struct {
	DocumentID string
	Expected   int
	Actual     int
}

func (e *ReindexRequiredError) Error() string {
	if e == nil {
		return "postgres vector store: reindex required"
	}
	id := e.DocumentID
	if id == "" {
		id = "?"
	}
	return fmt.Sprintf("postgres vector store: document %q has dimension %d, expected %d (reindex required)", id, e.Actual, e.Expected)
}

func (e *ReindexRequiredError) Is(target error) bool {
	return target == ErrDimensionMismatch
}

func (e *ReindexRequiredError) Unwrap() error {
	return ErrDimensionMismatch
}
