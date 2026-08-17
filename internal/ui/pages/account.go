package pages

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// AccountOrder is one row of the order history.
type AccountOrder struct {
	Number     string
	Status     string
	PlacedAt   string
	TotalCents int64
	LineCount  int64
	Committed  bool
	OwedCents  int64
}

// Total is what the order came to.
func (o AccountOrder) Total() string { return twd(o.TotalCents) }

// LineCountText is how many lines it holds.
func (o AccountOrder) LineCountText() string { return strconv.FormatInt(o.LineCount, 10) }

// StatusText is the fulfilment state in the chrome language.
func (o AccountOrder) StatusText(ctx context.Context) string {
	switch o.Status {
	case "pending":
		// 'pending' is two states wearing one name: nobody has paid yet, and the
		// money has arrived but nobody at the shop has picked it.
		if o.Committed || o.OwedCents <= 0 {
			return i18n.T(ctx, i18n.KeyStatusPaid)
		}
		return i18n.T(ctx, i18n.KeyStatusAwaitingPayment)
	case "picking":
		return i18n.T(ctx, i18n.KeyStatusPicking)
	case "shipped":
		return i18n.T(ctx, i18n.KeyStatusShipped)
	case "delivered":
		return i18n.T(ctx, i18n.KeyStatusDelivered)
	case "completed":
		return i18n.T(ctx, i18n.KeyStatusDone)
	case "cancelled":
		return i18n.T(ctx, i18n.KeyStatusCalledOff)
	default:
		return o.Status
	}
}

// AccountAddress is one saved delivery address.
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

// Line is the address as one line.
func (a AccountAddress) Line() string {
	return a.PostalCode + " " + a.City + a.District + a.Street
}

// DisplayLabel is the address's own name, or a stand-in.
func (a AccountAddress) DisplayLabel(ctx context.Context) string {
	if a.Label == "" {
		return i18n.T(ctx, i18n.KeyDeliveryToAddress)
	}
	return a.Label
}

// AccountView is the account landing page.
type AccountView struct {
	EmailVerified bool
	// PendingEmail is an address waiting to be proved, shown so somebody who
	// mistyped a change can see what they typed.
	PendingEmail string
	Email        string
	Name         string
	Phone        string
	Orders       []AccountOrder
	Addresses    []AccountAddress
	CreditCents  int64
	Standing     MemberStanding
	Notice       string
	GoogleLinked bool
	// CanUnlinkGoogle is false when Google is the only way into this account: one
	// with no password and no identity is one nobody can reach.
	CanUnlinkGoogle bool
}

// AccountMeta is the chrome view model for the account pages.
func AccountMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyAccountTitle)}
}

// DisplayName is the customer's name, or their email when they have not given
// one.
func (v *AccountView) DisplayName() string {
	if v.Name == "" {
		return v.Email
	}
	return v.Name
}

// Credit is the store-credit balance.
func (v *AccountView) Credit() string { return twd(v.CreditCents) }

// HasCredit reports whether there is any balance worth showing.
func (v *AccountView) HasCredit() bool { return v.CreditCents > 0 }

// HasOrders reports whether this account has ever ordered.
func (v *AccountView) HasOrders() bool { return len(v.Orders) > 0 }

// HasAddresses reports whether any address is saved.
func (v *AccountView) HasAddresses() bool { return len(v.Addresses) > 0 }

// HasNotice reports whether to show the confirmation banner.
func (v *AccountView) HasNotice() bool { return v.Notice != "" }

// AccountOrderView is one order, seen by its owner.
type AccountOrderView struct {
	Number         string
	Status         string
	PlacedAt       string
	ShippingName   string
	Lines          []OrderLine
	SubtotalCents  int64
	ShippingCents  int64
	DiscountCents  int64
	DiscountReason string
	TaxCents       int64
	Recipient      string
	Phone          string
	Email          string
	Address        string
	Committed      bool
	OwedCents      int64
}

// Subtotal is what the lines came to before shipping.
func (v *AccountOrderView) Subtotal() string { return twd(v.SubtotalCents) }

// Shipping is what delivery cost.
func (v *AccountOrderView) Shipping(ctx context.Context) string {
	if v.ShippingCents == 0 {
		return i18n.T(ctx, i18n.KeyFreeShipping)
	}
	return twd(v.ShippingCents)
}

// Discounted reports whether anything came off this order.
func (v *AccountOrderView) Discounted() bool { return v.DiscountCents > 0 }

// Discount is what came off, as a negative figure.
func (v *AccountOrderView) Discount() string { return "-" + twd(v.DiscountCents) }

// Total is what the order came to.
func (v *AccountOrderView) Total() string {
	return twd(v.SubtotalCents - v.DiscountCents + v.ShippingCents + v.TaxCents)
}

// StatusText is the fulfilment state in the chrome language. The funding fields
// travel with the status, or this page and the list it was reached from badge the
// same order differently.
func (v *AccountOrderView) StatusText(ctx context.Context) string {
	return AccountOrder{
		Status: v.Status, Committed: v.Committed, OwedCents: v.OwedCents,
	}.StatusText(ctx)
}

// AwaitingPayment reports whether this order still needs paying.
func (v *AccountOrderView) AwaitingPayment() bool {
	return v.Status == "pending" && !v.Committed && v.OwedCents > 0
}

// CanRegisterWarranty reports whether to offer the registration form. Both
// statuses that end a delivery, because convenience-store pickup moves
// shipped → completed with nobody at the counter to witness a handover.
func (v *AccountOrderView) CanRegisterWarranty() bool {
	switch v.Status {
	case "delivered", "completed":
		return true
	default:
		return false
	}
}

// WarrantyLink is where that form lives.
func (v *AccountOrderView) WarrantyLink() string { return "/account/warranty/" + v.Number }

// AuthView is the sign-in and registration form.
type AuthView struct {
	Email        string
	Name         string
	Next         string
	Errors       map[string]string
	Notice       string
	GoogleSignIn bool
}

// GoogleLink is where the button goes, carrying wherever the visitor was headed.
func (v AuthView) GoogleLink() string {
	if v.Next == "" {
		return "/auth/google"
	}
	return "/auth/google?next=" + url.QueryEscape(v.Next)
}

// SignInMeta is the chrome view model for the sign-in page.
func SignInMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeySignIn)}
}

// RegisterMeta is the chrome view model for the registration page.
func RegisterMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyRegister)}
}

// Err returns the message for a field, or "".
func (v AuthView) Err(field string) string { return v.Errors[field] }

// HasErr reports whether a field was rejected.
func (v AuthView) HasErr(field string) bool { return v.Errors[field] != "" }

// Invalid is the aria-invalid value for a field.
func (v AuthView) Invalid(field string) string {
	if v.HasErr(field) {
		return "true"
	}
	return "false"
}

// AnyErrors reports whether the form was rejected at all.
func (v AuthView) AnyErrors() bool { return len(v.Errors) > 0 }

// HasNotice reports whether to show the banner.
func (v AuthView) HasNotice() bool { return v.Notice != "" }

// MemberStanding is a customer's membership tier and what the next one asks for.
type MemberStanding struct {
	SpendCents     int64
	TierName       string
	MultiplierBP   int32
	NextName       string
	NextNeedsCents int64
}

// HasTier reports whether the customer has reached any band.
func (m MemberStanding) HasTier() bool { return m.TierName != "" }

// Spend is what they have spent in the window.
func (m MemberStanding) Spend() string { return twd(m.SpendCents) }

// Multiplier is what a point is worth here, in words, rendered from basis points
// so it cannot disagree with the number the capture multiplies by.
func (m MemberStanding) Multiplier(ctx context.Context) string {
	whole := m.MultiplierBP / 10000
	frac := (m.MultiplierBP % 10000) / 1000
	n := strconv.FormatInt(int64(whole), 10)
	if frac != 0 {
		n += "." + strconv.FormatInt(int64(frac), 10)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyMultiplierTimes), n)
}

// HasNext reports whether there is a band above this one.
func (m MemberStanding) HasNext() bool { return m.NextName != "" }

// NextNeeds is how much more the next band asks for.
func (m MemberStanding) NextNeeds() string { return twd(m.NextNeedsCents) }
