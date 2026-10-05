package orders

import (
	"fmt"
	"maps"
	"slices"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// timelineSource is the table AdminOrderTimeline read an entry from.
type timelineSource string

const (
	timelineOrder    timelineSource = "order"
	timelineInvoice  timelineSource = "invoice"
	timelineProvider timelineSource = "provider"
	timelineMail     timelineSource = "mail"
)

var (
	invoiceOperationLabels = map[string]i18n.Key{
		"issue":     i18n.KeyAuditInvoiceIssue,
		"allowance": i18n.KeyAuditInvoiceAllowance,
		"void":      i18n.KeyAuditInvoiceVoid,
	}
	invoiceOperationStatuses = map[string]i18n.Key{
		"pending":        i18n.KeyAdminTimelineInvoicePending,
		"awaiting_buyer": i18n.KeyAdminTimelineInvoiceAwaitingBuyer,
		"attention":      i18n.KeyAdminTimelineInvoiceAttention,
		"succeeded":      i18n.KeyAdminTimelineInvoiceSucceeded,
		"rejected":       i18n.KeyAdminTimelineInvoiceRejected,
	}
	// orderMailLabels holds every topic the timeline lists. invoice.due and
	// invoice.void_due name the order too, but they only start an invoice
	// operation, which is listed in its own right.
	orderMailLabels = map[string]i18n.Key{
		outbox.TopicOrderPlaced.Name():   i18n.KeyAdminTimelineMailPlaced,
		outbox.TopicOrderPaid.Name():     i18n.KeyAdminTimelineMailPaid,
		outbox.TopicOrderShipped.Name():  i18n.KeyAdminTimelineMailShipped,
		outbox.TopicOrderTerminal.Name(): i18n.KeyAdminTimelineMailTerminal,
	}
	orderMailTopics = slices.Collect(maps.Keys(orderMailLabels))
	mailStatuses    = map[string]i18n.Key{
		"sent":   i18n.KeyAdminTimelineMailSent,
		"queued": i18n.KeyAdminTimelineMailQueued,
	}
)

func timelineEntry(r *db.AdminOrderTimelineRow) (admin.TimelineEntry, error) {
	e := admin.TimelineEntry{
		At: shoptime.Minute(r.At), Note: r.Note,
		ActorKind: admin.ActorKind(r.ActorKind), Actor: r.ActorName,
	}
	var labels, statuses map[string]i18n.Key
	switch timelineSource(r.Source) {
	case timelineOrder:
		e.Label = pages.OrderEvent{Kind: order.EventKind(r.Kind)}.LabelKey()
		return e, nil
	case timelineProvider:
		e.Label = i18n.KeyAdminTimelineProvider
		return e, nil
	case timelineInvoice:
		labels, statuses = invoiceOperationLabels, invoiceOperationStatuses
	case timelineMail:
		labels, statuses = orderMailLabels, mailStatuses
	}
	label, labelled := labels[r.Kind]
	status, statused := statuses[r.Status]
	if !labelled || !statused {
		return admin.TimelineEntry{}, fmt.Errorf("no timeline label for %s %q in status %q", r.Source, r.Kind, r.Status)
	}
	e.Label, e.Status = label, status
	e.DoneAt = nullableStamp(r.DoneAt)
	return e, nil
}
