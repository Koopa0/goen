package orders

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRefusedIfNoRowRefusesOnlyAMissingRow(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		err     error
		refused bool
	}{
		{name: "no row", err: pgx.ErrNoRows, refused: true},
		{name: "lock timeout", err: &pgconn.PgError{Code: "55P03"}, refused: false},
	} {
		got := refusedIfNoRow(tt.err, "read the row")
		if errors.Is(got, ErrRefused) != tt.refused {
			t.Errorf("refusedIfNoRow(%s) = %v, refused %t, want %t", tt.name, got, !tt.refused, tt.refused)
		}
		if !errors.Is(got, tt.err) {
			t.Errorf("refusedIfNoRow(%s) = %v, which no longer wraps %v", tt.name, got, tt.err)
		}
	}
}
