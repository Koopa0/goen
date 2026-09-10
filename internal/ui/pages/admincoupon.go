package pages

import (
	"context"
	"fmt"

	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// AdminCoupon is one promotion as the back office sees it.
type AdminCoupon struct {
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
func (c AdminCoupon) Value(ctx context.Context) string {
	switch c.Kind {
	case "amount":
		return twd(c.AmountCents)
	case "percent":
		s := strconv.FormatInt(int64(c.PercentBP)/100, 10) + "%"
		if c.CapCents > 0 {
			s += fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponCap), twd(c.CapCents))
		}
		return s
	default:
		return i18n.T(ctx, i18n.KeyCouponKindShipping)
	}
}

// Conditions is the fine print: the minimum spend and the limits.
func (c AdminCoupon) Conditions(ctx context.Context) string {
	parts := make([]string, 0, 3)
	if c.MinSpend > 0 {
		parts = append(parts, fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponMin), twd(c.MinSpend)))
	}
	if c.MaxRedeem > 0 {
		parts = append(parts, fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponTotalLimit), c.MaxRedeem))
	}
	parts = append(parts, fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponPerPerson), c.PerCustomer))
	return strings.Join(parts, " · ")
}

// Used is how many times it has been redeemed, and what that has cost.
func (c AdminCoupon) Used(ctx context.Context) string {
	s := fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponUsed), c.Redeemed)
	if c.GivenCents > 0 {
		s += fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponGiven), twd(c.GivenCents))
	}
	return s
}

// State is whether the coupon works right now; active and current differ.
func (c AdminCoupon) State(ctx context.Context) string {
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
func (c AdminCoupon) Live() bool { return c.Active && c.Current }

// Action is where the on/off form posts.
func (c AdminCoupon) Action() string { return "/admin/coupons/" + c.Code + "/active" }

// NextActive is what the toggle would set it to.
func (c AdminCoupon) NextActive() string {
	if c.Active {
		return "false"
	}
	return "true"
}

// ToggleLabel is what the button says.
func (c AdminCoupon) ToggleLabel(ctx context.Context) string {
	if c.Active {
		return i18n.T(ctx, i18n.KeyAdminToggleOff)
	}
	return i18n.T(ctx, i18n.KeyAdminToggleOn)
}

// AdminCouponsView is the promotions page.
type AdminCouponsView struct {
	Rows   []AdminCoupon
	Notice string
	Errors map[string]string
	Draft  AdminCouponDraft
}

// AdminCouponDraft carries a refused form's values back into it.
type AdminCouponDraft struct {
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
func (d AdminCouponDraft) IsKind(k string) bool {
	if d.Kind == "" {
		return k == "amount"
	}
	return d.Kind == k
}

// Empty reports whether no promotion has been issued yet.
func (v *AdminCouponsView) Empty() bool { return len(v.Rows) == 0 }

// HasErr reports whether a field was refused.
func (v *AdminCouponsView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

// Err is why a field was refused.
func (v *AdminCouponsView) Err(f string) string { return v.Errors[f] }
