package orders

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

func TestTimelineEntryKeepsARowItCannotLabel(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.October, 5, 10, 20, 0, 0, time.UTC)
	for _, tc := range []struct {
		name             string
		row              db.AdminOrderTimelineRow
		wantLabel        i18n.Key
		wantUnrecognized string
	}{
		{"known invoice operation", db.AdminOrderTimelineRow{Source: "invoice", Kind: "issue", Status: "succeeded"},
			i18n.KeyAuditInvoiceIssue, ""},
		{"issue nothing will send", db.AdminOrderTimelineRow{Source: "invoice", Kind: "issue", Status: "not_sent"},
			i18n.KeyAuditInvoiceIssue, ""},
		{"invoice status from a newer build", db.AdminOrderTimelineRow{Source: "invoice", Kind: "issue", Status: "voided"},
			i18n.KeyAdminTimelineUnrecognized, "invoice / issue / voided"},
		{"mail topic from a newer build", db.AdminOrderTimelineRow{Source: "mail", Kind: "order.refunded", Status: "sent"},
			i18n.KeyAdminTimelineUnrecognized, "mail / order.refunded / sent"},
		{"order event kind from a newer build", db.AdminOrderTimelineRow{Source: "order", Kind: "lost"},
			i18n.KeyAdminTimelineUnrecognized, "order / lost"},
		{"known mail", db.AdminOrderTimelineRow{Source: "mail", Kind: outbox.TopicOrderPaid.Name(), Status: "queued"},
			i18n.KeyAdminTimelineMailPaid, ""},
	} {
		tc.row.At = at
		got := timelineEntry(&tc.row)
		if got.Label != tc.wantLabel || got.Unrecognized != tc.wantUnrecognized {
			t.Errorf("timelineEntry(%s) = label %q unrecognized %q, want %q %q",
				tc.name, got.Label, got.Unrecognized, tc.wantLabel, tc.wantUnrecognized)
		}
		if got.At == "" {
			t.Errorf("timelineEntry(%s) lost its time", tc.name)
		}
	}
}

func TestAnIssueNothingWillSendIsNotInProgress(t *testing.T) {
	t.Parallel()
	got := timelineEntry(&db.AdminOrderTimelineRow{Source: "invoice", Kind: "issue", Status: "not_sent"})
	if got.Status != i18n.KeyAdminTimelineInvoiceNotSent {
		t.Errorf("timelineEntry(issue not_sent).Status = %q, want %q", got.Status, i18n.KeyAdminTimelineInvoiceNotSent)
	}
}

func TestLogUnrecognizedLogsOnlyUnrecognizedEntries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		timeline []admin.TimelineEntry
		want     int
	}{
		{"one unrecognized entry", []admin.TimelineEntry{{At: "10:00"}, {At: "10:05", Unrecognized: "invoice / issue / voided"}}, 1},
		{"every entry recognized", []admin.TimelineEntry{{At: "10:00"}}, 0},
	} {
		var buf bytes.Buffer
		logUnrecognized(t.Context(), slog.New(slog.NewTextHandler(&buf, nil)), "GO-261005-000001", tc.timeline)
		if got := strings.Count(buf.String(), "unrecognised order timeline entry"); got != tc.want {
			t.Errorf("logUnrecognized(%s) wrote %d records, want %d: %q", tc.name, got, tc.want, buf.String())
		}
	}
}

func TestTimelineEntryCarriesWhenAnOperationCompleted(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, time.October, 5, 10, 0, 0, 0, time.UTC)
	completed := pgtype.Timestamptz{Time: created.Add(20 * time.Minute), Valid: true}
	for _, tc := range []struct {
		name string
		row  db.AdminOrderTimelineRow
		want bool
	}{
		{"succeeded invoice operation", db.AdminOrderTimelineRow{Source: "invoice", Kind: "issue", Status: "succeeded", DoneAt: completed}, true},
		{"invoice operation still pending", db.AdminOrderTimelineRow{Source: "invoice", Kind: "issue", Status: "pending"}, false},
	} {
		tc.row.At = created
		got := timelineEntry(&tc.row)
		if (got.DoneAt != "") != tc.want || got.At == got.DoneAt {
			t.Errorf("timelineEntry(%s) At %q DoneAt %q, want a completion time = %v", tc.name, got.At, got.DoneAt, tc.want)
		}
	}
}
