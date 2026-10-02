package admin

import (
	"context"
	"fmt"

	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Coupon is one promotion as the back office sees it.
type Coupon struct {
	Code        string
	Description string
	Kind        string
	KindText    string
	AmountCents int64
	PercentBP   int32
	CapCents    int64
	MinSpend    int64
	MaxRedeem   int32
	PerCustomer int32
	Redeemed    int64
	GivenCents  int64
	Active      bool
	Current     bool
	EndsAt      string
}

// Value is what it takes off.
func (c Coupon) Value(ctx context.Context) string {
	switch c.Kind {
	case "amount":
		return money.TWD(c.AmountCents)
	case "percent":
		s := strconv.FormatInt(int64(c.PercentBP)/100, 10) + "%"
		if c.CapCents > 0 {
			s += fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponCap), money.TWD(c.CapCents))
		}
		return s
	default:
		return i18n.T(ctx, i18n.KeyCouponKindShipping)
	}
}

// Conditions is the fine print: the minimum spend and the limits.
func (c Coupon) Conditions(ctx context.Context) string {
	parts := make([]string, 0, 3)
	if c.MinSpend > 0 {
		parts = append(parts, fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponMin), money.TWD(c.MinSpend)))
	}
	if c.MaxRedeem > 0 {
		parts = append(parts, fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponTotalLimit), c.MaxRedeem))
	}
	parts = append(parts, fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponPerPerson), c.PerCustomer))
	return strings.Join(parts, " · ")
}

// Used is how many times it has been redeemed, and what that has cost.
func (c Coupon) Used(ctx context.Context) string {
	s := fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponUsed), c.Redeemed)
	if c.GivenCents > 0 {
		s += fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponGiven), money.TWD(c.GivenCents))
	}
	return s
}

// State is whether the coupon works right now; active and current differ.
func (c Coupon) State(ctx context.Context) string {
	switch {
	case !c.Active:
		return i18n.T(ctx, i18n.KeyAdminCouponOff)
	case !c.Current:
		return i18n.T(ctx, i18n.KeyAdminCouponOutside)
	default:
		return i18n.T(ctx, i18n.KeyAdminCouponLive)
	}
}

// Live reports whether a customer could use it this moment.
func (c Coupon) Live() bool { return c.Active && c.Current }

// Action is where the on/off form posts.
func (c Coupon) Action() string { return "/admin/coupons/" + c.Code + "/active" }

// NextActive is what the toggle would set it to.
func (c Coupon) NextActive() string {
	if c.Active {
		return "false"
	}
	return "true"
}

// ToggleLabel is what the button says.
func (c Coupon) ToggleLabel(ctx context.Context) string {
	if c.Active {
		return i18n.T(ctx, i18n.KeyAdminToggleOff)
	}
	return i18n.T(ctx, i18n.KeyAdminToggleOn)
}

// CouponsView is the promotions page.
type CouponsView struct {
	pages.ListBound

	Rows   []Coupon
	Notice string
	Errors map[string]string
	Draft  CouponDraft
}

// CouponDraft carries a refused form's values back into it.
type CouponDraft struct {
	Code        string
	Description string
	Kind        string
	Value       string
	Cap         string
	MinSpend    string
	MaxRedeem   string
	PerCustomer string
	Days        string
}

// IsKind reports whether k is the chosen kind.
func (d CouponDraft) IsKind(k string) bool {
	if d.Kind == "" {
		return k == "amount"
	}
	return d.Kind == k
}

// Empty reports whether no promotion has been issued yet.
func (v *CouponsView) Empty() bool { return len(v.Rows) == 0 }

// HasErr reports whether a field was refused.
func (v *CouponsView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

// Err is why a field was refused.
func (v *CouponsView) Err(f string) string { return v.Errors[f] }
