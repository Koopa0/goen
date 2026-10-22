package reports

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/product/inventory"
)

// recordedCover remembers the window it was asked for and fails the report
// there, so ReportAt returns before it touches a pool.
type recordedCover struct{ days int }

var errCover = errors.New("cover stopped")

func (c *recordedCover) DaysCover(_ context.Context, days int, _ time.Time) ([]inventory.Risk, int, error) {
	c.days = days
	return nil, 0, errCover
}

func TestNewStoreRequiresItsReaders(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		pool  *pgxpool.Pool
		cover Cover
	}{
		{"no pool", nil, &recordedCover{}},
		{"no cover", &pgxpool.Pool{}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recover() == nil {
					t.Error("NewStore returned without panicking")
				}
			}()
			NewStore(tt.pool, tt.cover)
		})
	}
}

// TestStore_ReportAtCoverWindow: report asks the cover for a whole report
// window.
func TestStore_ReportAtCoverWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 7, 20, 0, 0, time.UTC)
	for _, tt := range []struct {
		name string
		days int32
		want int
	}{
		{"a listed window", 90, 90},
		{"the shortest window", 7, 7},
		{"a window the report does not offer reads the default", 5, int(DefaultWindow)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cover := &recordedCover{}
			s := &Store{cover: cover}
			if _, err := s.ReportAt(t.Context(), tt.days, now); !errors.Is(err, errCover) {
				t.Fatalf("ReportAt(%d) = %v, want the cover's error", tt.days, err)
			}
			if cover.days != tt.want {
				t.Errorf("ReportAt(%d) asked the cover for %d days, want %d", tt.days, cover.days, tt.want)
			}
		})
	}
}
