package orders

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
)

func TestWorkRow_Waited(t *testing.T) {
	t.Parallel()
	taipei := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 12, 0, 1, 0, 0, taipei)
	for _, tt := range []struct {
		name    string
		since   time.Time
		days    int
		overdue bool
		zh, en  string
	}{
		{"paid today", time.Date(2026, 10, 12, 0, 0, 30, 0, taipei), 0, false, "今天", "Today"},
		{"two minutes across midnight", time.Date(2026, 10, 11, 23, 59, 0, 0, taipei), 1, false, "已等 1 天", "Waiting 1 day"},
		{"the second day", time.Date(2026, 10, 10, 12, 0, 0, 0, taipei), 2, false, "已等 2 天", "Waiting 2 days"},
		{"the third day is overdue", time.Date(2026, 10, 9, 23, 0, 0, 0, taipei), 3, true, "已等 3 天", "Waiting 3 days"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := workRow(Row{Number: "GO-261012-AAAAAA"}, tt.since, now)
			if w.WaitDays != tt.days || w.Overdue != tt.overdue {
				t.Fatalf("waited %d days, overdue %t; want %d, %t", w.WaitDays, w.Overdue, tt.days, tt.overdue)
			}
			if got := w.WaitText(i18n.WithLocale(t.Context(), i18n.ZhHant)); got != tt.zh {
				t.Errorf("zh-Hant = %q, want %q", got, tt.zh)
			}
			if got := w.WaitText(i18n.WithLocale(t.Context(), i18n.En)); got != tt.en {
				t.Errorf("en = %q, want %q", got, tt.en)
			}
		})
	}
}

// A position handed out while the work tabs ran newest first named its time At.
// It must read back as no position, so the reader is told the page is gone
// rather than carried on from the wrong place.
func TestWorkPosition_OldCursorIsRefused(t *testing.T) {
	t.Parallel()
	const scope = "/admin/orders?status=ready"
	id := uuid.NewString()
	old := fmt.Sprintf(`{"At":"2026-10-01T04:00:00+00:00","ID":%q}`, id)
	current := fmt.Sprintf(`{"Since":"2026-10-01T04:00:00+00:00","ID":%q}`, id)
	for _, tt := range []struct {
		name, position string
		want           bool
	}{
		{"placed_at-era cursor", old, false},
		{"current cursor", current, true},
	} {
		next, ok := keyset.NextURL(scope, tt.position)
		if !ok {
			t.Fatalf("%s: no token", tt.name)
		}
		u, err := url.Parse(next)
		if err != nil {
			t.Fatal(err)
		}
		after := u.Query().Get(keyset.Param)
		if _, got := keyset.Resume(scope, after, usableWorkPosition); got != tt.want {
			t.Errorf("%s: read back = %t, want %t", tt.name, got, tt.want)
		}
	}
}
