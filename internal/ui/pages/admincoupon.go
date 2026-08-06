package pages

import (
	"strconv"
	"strings"
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

// Value is what it takes off, written the way somebody running the promotion
// would say it.
func (c AdminCoupon) Value() string {
	switch c.Kind {
	case "amount":
		return twd(c.AmountCents)
	case "percent":
		s := strconv.FormatInt(int64(c.PercentBP)/100, 10) + "%"
		if c.CapCents > 0 {
			s += "(上限 " + twd(c.CapCents) + ")"
		}
		return s
	default:
		return "免運"
	}
}

// Conditions is the fine print: the minimum spend and the limits.
func (c AdminCoupon) Conditions() string {
	parts := make([]string, 0, 3)
	if c.MinSpend > 0 {
		parts = append(parts, "滿 "+twd(c.MinSpend))
	}
	if c.MaxRedeem > 0 {
		parts = append(parts, "限量 "+strconv.FormatInt(int64(c.MaxRedeem), 10))
	}
	parts = append(parts, "每人 "+strconv.FormatInt(int64(c.PerCustomer), 10)+" 次")
	return strings.Join(parts, " · ")
}

// Used is how many times it has been redeemed, and what that has cost.
func (c AdminCoupon) Used() string {
	s := strconv.FormatInt(c.Redeemed, 10) + " 次"
	if c.GivenCents > 0 {
		s += " · 已折抵 " + twd(c.GivenCents)
	}
	return s
}

// State is the one thing a staff member scans for: whether it works right now.
//
// Active and current are different facts — a switched-on coupon whose window
// has passed is off to a customer and on in the list, which is how somebody
// spends an afternoon wondering why a code does not work.
func (c AdminCoupon) State() string {
	switch {
	case !c.Active:
		return "已停用"
	case !c.Current:
		return "不在期間內"
	default:
		return "使用中"
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
func (c AdminCoupon) ToggleLabel() string {
	if c.Active {
		return "停用"
	}
	return "啟用"
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
