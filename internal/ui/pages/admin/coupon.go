package admin

import (
	"context"
	"fmt"

	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/coupon"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/web"
)

type Coupon struct {
	Code        string
	Description string
	Kind        coupon.Kind
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

func (c Coupon) Value(ctx context.Context) string {
	switch c.Kind {
	case coupon.Amount:
		return money.TWD(c.AmountCents)
	case coupon.Percent:
		s := strconv.FormatInt(int64(c.PercentBP)/100, 10) + "%"
		if c.CapCents > 0 {
			s += fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponCap), money.TWD(c.CapCents))
		}
		return s
	case coupon.FreeShipping:
		return i18n.T(ctx, i18n.KeyCouponKindShipping)
	}
	return ""
}

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

func (c Coupon) Used(ctx context.Context) string {
	s := fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponUsed), c.Redeemed)
	if c.GivenCents > 0 {
		s += fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponGiven), money.TWD(c.GivenCents))
	}
	return s
}

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

func (c Coupon) Live() bool { return c.Active && c.Current }

func (c Coupon) Action() string { return "/admin/coupons/" + c.Code + "/active" }

func (c Coupon) NextActive() string {
	if c.Active {
		return "false"
	}
	return "true"
}

func (c Coupon) ToggleLabel(ctx context.Context) string {
	if c.Active {
		return i18n.T(ctx, i18n.KeyAdminToggleOff)
	}
	return i18n.T(ctx, i18n.KeyAdminToggleOn)
}

type CouponsView struct {
	web.Bound

	Rows   []Coupon
	Notice string
	Errors map[string]string
	Draft  CouponDraft
}

type CouponDraft struct {
	Code        string
	Description string
	Kind        coupon.Kind
	Value       string
	Cap         string
	MinSpend    string
	MaxRedeem   string
	PerCustomer string
	Days        string
}

func (d CouponDraft) IsKind(k coupon.Kind) bool {
	if d.Kind == "" {
		return k == coupon.Amount
	}
	return d.Kind == k
}

func (v *CouponsView) Empty() bool { return len(v.Rows) == 0 }

func (v *CouponsView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

func (v *CouponsView) Err(f string) string { return v.Errors[f] }
