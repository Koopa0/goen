package orders

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
)

func TestTimelineEntryCarriesWhenAnOperationSettled(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, time.October, 5, 10, 0, 0, 0, time.UTC)
	settled := pgtype.Timestamptz{Time: created.Add(20 * time.Minute), Valid: true}
	for _, tc := range []struct {
		name string
		row  db.AdminOrderTimelineRow
		want bool
	}{
		{"succeeded invoice operation", db.AdminOrderTimelineRow{Source: "invoice", Kind: "issue", Status: "succeeded", DoneAt: settled}, true},
		{"invoice operation still pending", db.AdminOrderTimelineRow{Source: "invoice", Kind: "issue", Status: "pending"}, false},
	} {
		tc.row.At = created
		got, err := timelineEntry(&tc.row)
		if err != nil {
			t.Fatalf("timelineEntry(%s): %v", tc.name, err)
		}
		if (got.DoneAt != "") != tc.want || got.At == got.DoneAt {
			t.Errorf("timelineEntry(%s) At %q DoneAt %q, want a completion time = %v", tc.name, got.At, got.DoneAt, tc.want)
		}
	}
}
