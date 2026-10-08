package orders

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"

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

// timelineEntry labels one row. A row this build cannot label is shown as such
// rather than failing the page: the actions below the timeline read their own
// facts.
func timelineEntry(r *db.AdminOrderTimelineRow) admin.TimelineEntry {
	e := admin.TimelineEntry{
		At: shoptime.Minute(r.At), Note: r.Note,
		ActorKind: admin.ActorKind(r.ActorKind), Actor: r.ActorName,
	}
	var labels, statuses map[string]i18n.Key
	switch timelineSource(r.Source) {
	case timelineOrder:
		label, known := pages.OrderEvent{Kind: order.EventKind(r.Kind)}.LookupLabelKey()
		if !known {
			return unrecognizedEntry(e, r)
		}
		e.Label = label
		return e
	case timelineProvider:
		e.Label = i18n.KeyAdminTimelineProvider
		return e
	case timelineInvoice:
		labels, statuses = admin.InvoiceOperationLabels, admin.InvoiceOperationStatuses
	case timelineMail:
		labels, statuses = orderMailLabels, mailStatuses
	}
	label, labelled := labels[r.Kind]
	status, statused := statuses[r.Status]
	if !labelled || !statused {
		return unrecognizedEntry(e, r)
	}
	e.Label, e.Status = label, status
	e.DoneAt = nullableStamp(r.DoneAt)
	return e
}

func logUnrecognized(ctx context.Context, log *slog.Logger, number string, timeline []admin.TimelineEntry) {
	for i := range timeline {
		if e := &timeline[i]; e.Unrecognized != "" {
			log.WarnContext(ctx, "unrecognised order timeline entry",
				"order", number, "entry", e.Unrecognized, "at", e.At)
		}
	}
}

func unrecognizedEntry(e admin.TimelineEntry, r *db.AdminOrderTimelineRow) admin.TimelineEntry {
	e.Label = i18n.KeyAdminTimelineUnrecognized
	e.Unrecognized = strings.Join(slices.DeleteFunc([]string{r.Source, r.Kind, r.Status},
		func(s string) bool { return s == "" }), " / ")
	return e
}
