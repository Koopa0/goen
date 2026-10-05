package orders

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

func TestTimelineEntryKeepsARowItCannotLabel(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.October, 5, 10, 20, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		row        db.AdminOrderTimelineRow
		wantLabel  i18n.Key
		wantUnread string
	}{
		{"known invoice operation", db.AdminOrderTimelineRow{Source: "invoice", Kind: "issue", Status: "succeeded"},
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
		if got.Label != tc.wantLabel || got.Unrecognized != tc.wantUnread {
			t.Errorf("timelineEntry(%s) = label %q unrecognized %q, want %q %q",
				tc.name, got.Label, got.Unrecognized, tc.wantLabel, tc.wantUnread)
		}
		if got.At == "" {
			t.Errorf("timelineEntry(%s) lost its time", tc.name)
		}
	}
}
