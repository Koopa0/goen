package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// PointsEntry is one line of the ledger.
type PointsEntry struct {
	Points    int64
	Reason    string
	Order     string
	At        string
	ExpiresOn string
	Expired   bool
}

// Earned reports whether this line added points.
func (e PointsEntry) Earned() bool { return e.Points > 0 }

// Amount is the change, signed so a ledger reads as one.
func (e PointsEntry) Amount() string {
	if e.Points > 0 {
		return "+" + strconv.FormatInt(e.Points, 10)
	}
	return strconv.FormatInt(e.Points, 10)
}

// What describes the line in the chrome language.
func (e PointsEntry) What(ctx context.Context) string {
	switch e.Reason {
	case "order":
		if e.Order != "" {
			return fmt.Sprintf(i18n.T(ctx, i18n.KeyPointsFromOrder), e.Order)
		}
		return i18n.T(ctx, i18n.KeyPointsEarned)
	case "redeem":
		return i18n.T(ctx, i18n.KeyPointsSpent)
	default:
		return e.Reason
	}
}

// PointsView is the customer's points page.
type PointsView struct {
	Balance     int64
	Redeemable  int64
	CreditCents int64

	ExpiringPoints int64
	ExpiringOn     string
	WarningDays    int

	PerCredit int64
	Minimum   int64

	Entries []PointsEntry
	Notice  string
}

// BalanceText is the spendable balance.
func (v PointsView) BalanceText() string { return strconv.FormatInt(v.Balance, 10) }

// RedeemableText is what can be exchanged now.
func (v PointsView) RedeemableText() string { return strconv.FormatInt(v.Redeemable, 10) }

// Credit is what that exchange is worth.
func (v PointsView) Credit() string { return twd(v.CreditCents) }

// CanRedeem reports whether the form should be offered.
func (v PointsView) CanRedeem() bool { return v.Redeemable > 0 }

// MinimumText is the smallest redemption.
func (v PointsView) MinimumText() string { return strconv.FormatInt(v.Minimum, 10) }

// RateText is the exchange rate in words.
func (v PointsView) RateText(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyPointsRate), strconv.FormatInt(v.PerCredit, 10))
}

// Expiring reports whether anything is about to lapse.
func (v PointsView) Expiring() bool { return v.ExpiringPoints > 0 && v.ExpiringOn != "" }

// ExpiringText warns about it.
func (v PointsView) ExpiringText(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyPointsExpiring),
		strconv.FormatInt(v.ExpiringPoints, 10), v.ExpiringOn)
}

// Empty reports whether nothing has ever happened.
func (v PointsView) Empty() bool { return len(v.Entries) == 0 }

// StepText is the increment the redemption field accepts: one whole exchange.
func (v PointsView) StepText() string { return strconv.FormatInt(v.PerCredit, 10) }
