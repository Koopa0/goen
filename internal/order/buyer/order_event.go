package buyer

import (
	"context"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shoptime"
)

// OrderEvent carries no actor: anyone holding the order number can reach this page.
type OrderEvent struct {
	Kind order.EventKind
	Note string
	At   time.Time
	// Unsent marks the refunded event of units the shop could not send.
	Unsent bool
}

// ShowsNote reports whether the note belongs on the customer's history. A shipped event's note is the
// carrier's name and tracking number written in the shop's language, which the parcel already states in the
// reader's own.
func (e OrderEvent) ShowsNote() bool { return e.Note != "" && e.Kind != order.EventShipped }

// Stamp is the short date and time of an event, with the form a time element reads.
func (e OrderEvent) Stamp(ctx context.Context) string { return shoptime.StampText(ctx, e.At) }

func (e OrderEvent) Datetime() string { return shoptime.ISOStamp(e.At) }

// LabelKey is the short word the order's own history uses for the event.
func (e OrderEvent) LabelKey() i18n.Key {
	switch e.Kind {
	case order.EventPlaced:
		return i18n.KeyEventPlaced
	case order.EventPaid:
		return i18n.KeyStatusPaid
	case order.EventPicking:
		return i18n.KeyEventPicking
	case order.EventShipped:
		return i18n.KeyEventShipped
	case order.EventInTransit:
		return i18n.KeyStatusInTransit
	case order.EventDelivered:
		return i18n.KeyEventDelivered
	case order.EventCompleted:
		return i18n.KeyStatusCompleted
	case order.EventCancelled:
		return i18n.KeyEventCancelled
	case order.EventRefunded:
		if e.Unsent {
			return i18n.KeyEventRefundedUnsent
		}
		return i18n.KeyStatusRefunded
	default:
		panic("buyer: no label for order event kind " + string(e.Kind))
	}
}
