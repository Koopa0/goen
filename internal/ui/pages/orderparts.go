package pages

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shipping"
	"github.com/koopa0/goen/internal/shoptime"
)

type Delivery struct {
	PostalCode string
	City       string
	District   string
	Street     string

	PickupChain     shipping.Chain
	PickupStoreCode string
	PickupStoreName string
}

// IsPickup reads the chain as the destination: the store behind it comes from the
// carrier's picker and is absent on an order placed before one existed.
func (d Delivery) IsPickup() bool { return d.PickupChain != "" }

func (d Delivery) Line() string {
	if d.IsPickup() {
		line := d.PickupChain.Name()
		if d.PickupStoreName != "" {
			line += " " + d.PickupStoreName
		}
		if d.PickupStoreCode != "" {
			line += "(" + d.PickupStoreCode + ")"
		}
		return line
	}
	return strings.TrimSpace(d.PostalCode + " " + d.City + d.District + d.Street)
}

type OrderLine struct {
	SKU         string
	Name        string
	Label       string
	UnitCents   int64
	Quantity    int32
	ImageURL    string
	ImageSrcset string
	ImageAlt    string
	// WarrantyMonths is the promise the line copied at checkout; zero when it carried none.
	WarrantyMonths int
	// Registered counts the units this line's share has registered, and WarrantyUntil is the day their cover ends.
	Registered    int
	WarrantyUntil time.Time
}

func (l OrderLine) HasImage() bool { return l.ImageURL != "" }

func (l OrderLine) UnitPrice() string { return money.TWD(l.UnitCents) }

func (l OrderLine) LineTotal() string { return money.TWD(l.UnitCents * int64(l.Quantity)) }

func (l OrderLine) QuantityText() string { return strconv.FormatInt(int64(l.Quantity), 10) }

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
		panic("pages: no label for order event kind " + string(e.Kind))
	}
}

// LookupLabelKey is LabelKey for a reader that must survive a kind it does not
// know.
func (e OrderEvent) LookupLabelKey() (i18n.Key, bool) {
	switch e.Kind {
	case order.EventPlaced:
		return i18n.KeyStatusPlaced, true
	case order.EventPaid:
		return i18n.KeyStatusPaid, true
	case order.EventPicking:
		return i18n.KeyStatusPicking, true
	case order.EventShipped:
		return i18n.KeyStatusShipped, true
	case order.EventInTransit:
		return i18n.KeyStatusInTransit, true
	case order.EventDelivered:
		return i18n.KeyStatusDelivered, true
	case order.EventCompleted:
		return i18n.KeyStatusCompleted, true
	case order.EventCancelled:
		return i18n.KeyStatusCancelled, true
	case order.EventRefunded:
		return i18n.KeyStatusRefunded, true
	default:
		return "", false
	}
}
