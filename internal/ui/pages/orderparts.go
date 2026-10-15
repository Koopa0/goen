package pages

import (
	"strconv"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shipping"
)

type PickupChainChoice struct {
	Value shipping.Chain
	Label string
}

// KnownPickupChainChoices keeps historical orders correctable when checkout no longer takes their chain.
func KnownPickupChainChoices() []PickupChainChoice {
	return choicesFor(shipping.Chains())
}

func choicesFor(chains []shipping.Chain) []PickupChainChoice {
	out := make([]PickupChainChoice, 0, len(chains))
	for _, c := range chains {
		out = append(out, PickupChainChoice{Value: c, Label: pickupChainLabel(c)})
	}
	return out
}

func pickupChainLabel(code shipping.Chain) string {
	switch code {
	case "":
		return ""
	case shipping.ChainSevenEleven:
		return "7-ELEVEN"
	case shipping.ChainFamilyMart:
		return "全家 FamilyMart" // i18n-exempt: a chain's own name, already bilingual
	case shipping.ChainHiLife:
		return "萊爾富 Hi-Life" // i18n-exempt: a chain's own name, already bilingual
	case shipping.ChainOKMart:
		return "OK mart"
	default:
		panic("pages: unknown pickup chain: " + string(code))
	}
}

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
		line := pickupChainLabel(d.PickupChain)
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

func (l OrderLine) UnitPrice() string { return twd(l.UnitCents) }

func (l OrderLine) LineTotal() string { return twd(l.UnitCents * int64(l.Quantity)) }

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
