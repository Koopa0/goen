package pages

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/web"
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
		dateStat(ctx, i18n.T(ctx, i18n.KeyOrderFactPlaced), o.PlacedAt, ""),
		{Label: i18n.T(ctx, i18n.KeyOrderGrandTotal), Value: components.StatMoney(o.TotalCents)},
	}
	if o.OneLastDay {
		facts = append(facts, dateStat(ctx, i18n.T(ctx, i18n.KeyOrderLastDay), o.LastDay, ""))
	}
	return facts
}

func (o AccountOrder) LineCountText() string { return strconv.FormatInt(o.LineCount, 10) }

// awaitingPayment is the one definition of an order that still needs paying.
// 'pending' alone misses an order paid wholly from store credit or zeroed by a
// coupon: it has no payment row and owes nothing.
func awaitingPayment(status order.FulfillmentStatus, committed bool, owedCents int64) bool {
	return status == order.FulfillmentPending && !committed && owedCents > 0
}

func (o AccountOrder) AwaitingPayment() bool {
	return awaitingPayment(o.Status, o.Committed, o.OwedCents)
}

func (o AccountOrder) StatusText(ctx context.Context) string {
	if o.Returned {
		return i18n.T(ctx, i18n.KeyStatusRefunded)
	}
	switch o.Status {
	case order.FulfillmentPending:
		if awaitingPayment(o.Status, o.Committed, o.OwedCents) {
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
	case awaitingPayment(o.Status, o.Committed, o.OwedCents):
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

	EmailVerified   bool
	PendingEmail    string
	Email           string
	Name            string
	Phone           string
	Orders          []AccountOrder
	OrdersBound     web.Bound
	Addresses       []AccountAddress
	CreditCents     int64
	Standing        MemberStanding
	Notice          string
	GoogleLinked    bool
	CanUnlinkGoogle bool
	PaymentsEnabled bool
	// Both are nil on a plain visit.
	AddressDraft  *AddressDraft
	AddressErrors map[string]string
}

type AddressDraft struct {
	Label, Name, Phone, PostalCode, City, District, Street string
	Default                                                bool
}

// AddressOpen keeps the disclosure open on a refused address so its messages show.
func (v *AccountView) AddressOpen() bool { return v.AddressDraft != nil }

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

type SignInRecovery uint8

const (
	SignInNoRecovery SignInRecovery = iota
	SignInResetPassword
	SignInRegister
)

func (r SignInRecovery) Href() string {
	switch r {
	case SignInNoRecovery:
		return ""
	case SignInResetPassword:
		return "/forgot"
	case SignInRegister:
		return "/register"
	default:
		panic(fmt.Sprintf("sign-in: unknown recovery %d", r))
	}
}

func (r SignInRecovery) Label(ctx context.Context) string {
	switch r {
	case SignInNoRecovery:
		return ""
	case SignInResetPassword:
		return i18n.T(ctx, i18n.KeyForgotPassword)
	case SignInRegister:
		return i18n.T(ctx, i18n.KeyRegister)
	default:
		panic(fmt.Sprintf("sign-in: unknown recovery %d", r))
	}
}

type AuthView struct {
	ReturnMessage string
	HideRegister  bool
	PasswordFocus bool
	Recovery      SignInRecovery

	Email        string
	Name         string
	Next         string
	Errors       map[string]string
	Notice       string
	GoogleSignIn bool
	OffersResend bool
	// DemoEmail and DemoPassword are the account every visitor of a public demonstration shares; empty is none.
	DemoEmail    string
	DemoPassword string
}

func (v AuthView) OffersDemoAccount() bool { return v.DemoEmail != "" }

type RegisterCompleteView struct {
	Token string
	Next  string
	Error string
}

func (v RegisterCompleteView) HasError() bool { return v.Error != "" }

func RegisterCompleteMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyRegisterCompleteTitle)}
}

func (v AuthView) GoogleLink() string {
	if v.Next == "" {
		return "/auth/google"
	}
	return "/auth/google?next=" + url.QueryEscape(v.Next)
}

func SignInMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeySignIn)}
}

func RegisterMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyRegister)}
}

func (v AuthView) Err(field string) string { return v.Errors[field] }

func (v AuthView) HasErr(field string) bool { return v.Errors[field] != "" }

func (v AuthView) AnyErrors() bool { return len(v.Errors) > 0 }

func (v AuthView) HasNotice() bool { return v.Notice != "" }

type MemberStanding struct {
	SpendCents     int64
	TierName       string
	MultiplierBP   int32
	NextName       string
	NextNeedsCents int64
}

func (m MemberStanding) HasTier() bool { return m.TierName != "" }

func (m MemberStanding) Spend() string { return twd(m.SpendCents) }

func (m MemberStanding) Multiplier(ctx context.Context) string {
	whole := m.MultiplierBP / 10000
	frac := m.MultiplierBP % 10000
	n := strconv.FormatInt(int64(whole), 10)
	if frac != 0 {
		n += "." + strings.TrimRight(fmt.Sprintf("%04d", frac), "0")
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyMultiplierTimes), n)
}

func (m MemberStanding) HasNext() bool { return m.NextName != "" }

func (m MemberStanding) NextNeeds() string { return twd(m.NextNeedsCents) }

type CartRecoveryView struct {
	Welcome bool
	Next    string
	Notice  string
	Retry   string
}

func CartRecoveryMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyCartMergeRecoveryTitle)}
}
