package cart

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/i18n"
)

// An undelivered parcel's day column holds shop_today(); it must never be read.
func TestAnUndeliveredParcelPrintsNoRescissionDay(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	day := time.Date(2099, 10, 9, 4, 0, 0, 0, time.UTC)
	if got := rescissionEnds(ctx, pgtype.Timestamptz{}, day); got != "" {
		t.Errorf("rescissionEnds(undelivered) = %q, want empty", got)
	}
	delivered := pgtype.Timestamptz{Time: day, Valid: true}
	if got, want := rescissionEnds(ctx, delivered, day), "Oct 9, 2099"; got != want {
		t.Errorf("rescissionEnds(delivered) = %q, want %q", got, want)
	}
}
