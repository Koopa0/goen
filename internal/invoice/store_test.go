package invoice

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestVoidClaimConstraintsAreMapped(t *testing.T) {
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

func TestAnUnmappedVoidClaimStaysUntyped(t *testing.T) {
	t.Parallel()

	err := mapVoidClaim("AB12345678", &pgconn.PgError{
		Code: "23514", ConstraintName: "invoice_void_something_else",
	})
	if errors.Is(err, ErrReason) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrRejected) {
		t.Fatalf("unknown constraint was classified: %v", err)
	}
}
