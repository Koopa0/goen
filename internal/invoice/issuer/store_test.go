package issuer

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestMapVoidClaim: a void claim refused by invoice_void_reason is ErrReason,
// and by invoice_void_target is ErrNotFound.
func TestMapVoidClaim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, constraint string
		want             error
	}{
		{name: "blank reason", constraint: "invoice_void_reason", want: ErrReason},
		{name: "already voided", constraint: "invoice_void_target", want: ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := mapVoidClaim("AB12345678", &pgconn.PgError{
				Code: "23514", ConstraintName: tt.constraint,
			})
			if !errors.Is(err, tt.want) {
				t.Fatalf("mapVoidClaim(%s) = %v, want %v", tt.constraint, err, tt.want)
			}
		})
	}
}

// TestMapVoidClaimUnmapped: a constraint mapVoidClaim does not know stays
// untyped: not ErrReason, ErrNotFound or ErrRejected.
func TestMapVoidClaimUnmapped(t *testing.T) {
	t.Parallel()

	err := mapVoidClaim("AB12345678", &pgconn.PgError{
		Code: "23514", ConstraintName: "invoice_void_something_else",
	})
	if errors.Is(err, ErrReason) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrRejected) {
		t.Fatalf("unknown constraint was classified: %v", err)
	}
}

// TestDueClaimOutcome: a due claim is done only when nothing is left to file;
// any other refusal holds the outbox message. Dropping it would leave a paid
// order uninvoiced with nothing on /admin/health to say so.
func TestDueClaimOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		done bool
	}{
		{name: "claimed", err: nil, done: true},
		{name: "already issued", err: &pgconn.PgError{
			Code: "23505", ConstraintName: "invoice_documents_one_active_invoice_per_order",
		}, done: true},
		{name: "nothing to file", err: &pgconn.PgError{
			Code: "23514", ConstraintName: "invoice_issue_itemisation",
		}, done: true},
		{name: "no filing snapshot", err: &pgconn.PgError{
			Code: "23514", ConstraintName: "invoice_issue_filing_snapshot",
		}, done: false},
		{name: "cancelled before the claim", err: &pgconn.PgError{
			Code: "23514", ConstraintName: "invoice_issue_committed",
		}, done: true},
		{name: "no such order", err: &pgconn.PgError{
			Code: "23514", ConstraintName: "invoice_issue_order",
		}, done: false},
		{name: "connection lost", err: errors.New("conn closed"), done: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := dueClaimOutcome("GO-261002-000001", tt.err); (got == nil) != tt.done {
				t.Fatalf("dueClaimOutcome(%v) = %v, want done=%t", tt.err, got, tt.done)
			}
		})
	}
}
