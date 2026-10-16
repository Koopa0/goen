package pages

import (
	"context"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/loyalty"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
)

type AccountOrder struct {
	Number     string
	Status     order.FulfillmentStatus
	PlacedAt   shoptime.Date
	TotalCents int64
	LineCount  int64
	Committed  bool
	OwedCents  int64
	// Returned is every unit of the order in a return whose refund has settled.
	Returned bool
	// OneLastDay is true when the whole order has a single last day to cancel; LastDay means nothing otherwise.
	OneLastDay bool
	LastDay    shoptime.Date
}

// Facts are what a row of the history says about its order. The last day to cancel is counted from each parcel's
// delivery, so the row names one only when the whole order has the same.
func (o AccountOrder) Facts(ctx context.Context) []components.Stat {
	facts := []components.Stat{
		components.DateStat(ctx, i18n.T(ctx, i18n.KeyOrderFactPlaced), o.PlacedAt, ""),
		{Label: i18n.T(ctx, i18n.KeyOrderGrandTotal), Value: components.StatMoney(o.TotalCents)},
	}
	if o.OneLastDay {
		facts = append(facts, components.DateStat(ctx, i18n.T(ctx, i18n.KeyOrderLastDay), o.LastDay, ""))
	}
	return facts
}

func (o AccountOrder) LineCountText() string { return strconv.FormatInt(o.LineCount, 10) }

func (o AccountOrder) AwaitingPayment() bool {
	return order.AwaitingPayment(o.Status, o.Committed, o.OwedCents)
}

func (o AccountOrder) StatusText(ctx context.Context) string {
	if o.Returned {
		return i18n.T(ctx, i18n.KeyStatusRefunded)
	}
	switch o.Status {
	case order.FulfillmentPending:
		if order.AwaitingPayment(o.Status, o.Committed, o.OwedCents) {
			return i18n.T(ctx, i18n.KeyStatusAwaitingPayment)
		}
		return i18n.T(ctx, i18n.KeyStatusPaid)
	case order.FulfillmentPicking:
		return i18n.T(ctx, i18n.KeyStatusPicking)
	case order.FulfillmentShipped:
		return i18n.T(ctx, i18n.KeyStatusShipped)
	case order.FulfillmentDelivered:
		return i18n.T(ctx, i18n.KeyStatusDelivered)
	case order.FulfillmentCompleted:
		return i18n.T(ctx, i18n.KeyStatusDone)
	case order.FulfillmentCancelled:
		return i18n.T(ctx, i18n.KeyStatusCalledOff)
	default:
		// A retired value from append-only history still has to render.
		return string(o.Status)
	}
}

// BadgeIntent colours only two states: one the shopper must act on and one that ended
// without a delivery. The rest is progress, not something to look at.
func (o AccountOrder) BadgeIntent() components.Intent {
	switch {
	case o.Status == order.FulfillmentCancelled:
		return components.IntentDanger
	case order.AwaitingPayment(o.Status, o.Committed, o.OwedCents):
		return components.IntentWarn
	default:
		return components.IntentNeutral
	}
}

type AccountAddress struct {
	ID         string
	Label      string
	Name       string
	Phone      string
	PostalCode string
	City       string
	District   string
	Street     string
	Default    bool
}

func (a AccountAddress) Line() string {
	return a.PostalCode + " " + a.City + a.District + a.Street
}

func (a AccountAddress) DisplayLabel(ctx context.Context) string {
	if a.Label == "" {
		return i18n.T(ctx, i18n.KeyDeliveryToAddress)
	}
	return a.Label
}

type AccountView struct {
	ReturnAfterWelcome string
	CartAdjusted       bool

	PasswordSetupSent          bool
	HasPassword                bool
	EmailVerified              bool
	EmailConfirmationRequested bool
	EmailReentry               bool
	PendingEmail               string
	Email                      string
	Name                       string
	Phone                      string
	Orders                     []AccountOrder
	OrdersBound                keyset.Bound
	Addresses                  []AccountAddress
	CreditCents                int64
	Standing                   loyalty.MemberStanding
	Notice                     string
	GoogleLinked               bool
	CanUnlinkGoogle            bool
	PaymentsEnabled            bool
	// Both are nil on a plain visit.
	AddressDraft  *AddressDraft
	AddressErrors map[string]string
}

type AddressDraft struct {
	ID string

	Label, Name, Phone, PostalCode, City, District, Street string
	Default                                                bool
}

func (v *AccountView) AddressOpen() bool { return v.AddressDraft != nil }

func (v *AccountView) AddressEditing() bool {
	return v.AddressDraft != nil && v.AddressDraft.ID != ""
}

func (v *AccountView) AddressAction() string {
	if v.AddressEditing() {
		return "/account/addresses/update#addresses-heading"
	}
	return "/account/addresses"
}

func (v *AccountView) AddressID() string {
	if v.AddressDraft == nil {
		return ""
	}
	return v.AddressDraft.ID
}

func (v *AccountView) AddrValue(field string) string {
	d := v.AddressDraft
	if d == nil {
		return ""
	}
	switch field {
	case "label":
		return d.Label
	case "name":
		return d.Name
	case "phone":
		return d.Phone
	case "postal_code":
		return d.PostalCode
	case "city":
		return d.City
	case "district":
		return d.District
	case "street":
		return d.Street
	}
	return ""
}

func (v *AccountView) AddrDefault() bool { return v.AddressDraft != nil && v.AddressDraft.Default }

func (v *AccountView) AddrInvalid(field string) bool { return v.AddressErrors[field] != "" }

func (v *AccountView) AddrErr(field string) string { return v.AddressErrors[field] }

func (v *AccountView) addressLabelDescribedBy() string {
	if v.AddrInvalid("label") {
		return "addr-label-example addr-label-error"
	}
	return "addr-label-example"
}

func (v *AccountView) addressLabelAttrs() templ.Attributes {
	attrs := templ.Attributes{"maxlength": "30", "autocomplete": "off"}
	if !v.AddrInvalid("label") {
		attrs["aria-describedby"] = v.addressLabelDescribedBy()
	}
	return attrs
}

func AccountMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyAccountTitle)}
}

func (v *AccountView) DisplayName() string {
	if v.Name == "" {
		return v.Email
	}
	return v.Name
}

func (v *AccountView) Credit() string { return twd(v.CreditCents) }

func (v *AccountView) HasCredit() bool { return v.CreditCents > 0 }

func (v *AccountView) HasOrders() bool { return len(v.Orders) > 0 }

func (v *AccountView) HasAddresses() bool { return len(v.Addresses) > 0 }

func (v *AccountView) HasNotice() bool { return v.Notice != "" }

func googlePasswordSetupText(ctx context.Context) (before, after string) {
	before, after, _ = strings.Cut(i18n.T(ctx, i18n.KeyGoogleOnlyMethod), "%s")
	return before, after
}

type CartRecoveryView struct {
	Welcome  bool
	Next     string
	Notice   string
	Retry    string
	Contents CartRecoveryContents
}

type CartRecoveryContents struct {
	BeforeSignIn []CartLine
	Account      []CartLine
}

func CartRecoveryMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyCartMergeRecoveryTitle)}
}
