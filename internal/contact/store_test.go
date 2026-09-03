package contact

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestDuplicateClassificationStaysInsideTheStoreBoundary(t *testing.T) {
	err := wrap("create contact message", &pgconn.PgError{
		Code: "23505", ConstraintName: "contact_messages_dedupe_key",
	})
	if !errors.Is(err, errDuplicate) {
		t.Fatalf("duplicate write = %v, want private duplicate classification", err)
	}
}
