package contact

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestWrapDuplicate: a duplicate write is classified inside the store boundary:
// wrap turns the dedupe constraint into the private errDuplicate.
func TestWrapDuplicate(t *testing.T) {
	err := wrap("create contact message", &pgconn.PgError{
		Code: "23505", ConstraintName: "contact_messages_dedupe_key",
	})
	if !errors.Is(err, errDuplicate) {
		t.Fatalf("duplicate write = %v, want private duplicate classification", err)
	}
}
