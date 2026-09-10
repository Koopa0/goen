package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// PointsEntryKind is what one ledger line did, closed by
// loyalty_entries_kind_shape. Declared here rather than in internal/loyalty
// because loyalty builds these view models, so a type it owned could not be
// named below without closing a cycle.
type PointsEntryKind string

// The three kinds a loyalty ledger line can be.
const (
	PointsAwarded    PointsEntryKind = "award"
	PointsSpent      PointsEntryKind = "spend"
	PointsClawedBack PointsEntryKind = "clawback"
)

// PointsEntry is one line of the ledger.
type PointsEntry struct {
	Points          int64
	RequestedPoints int64
	ShortfallPoints int64
	Kind            PointsEntryKind
	Order           string
	At              string
	ExpiresOn       string
	Expired         bool
}

// Earned reports whether this line added points.
func (e PointsEntry) Earned() bool {
	switch e.Kind {
	case PointsAwarded:
		return true
	case PointsSpent, PointsClawedBack:
		return false
	default:
		panic("pages: unknown points entry kind: " + string(e.Kind))
	}
}

// Amount is the change, signed so a ledger reads as one.
func (e PointsEntry) Amount() string {
	switch e.Kind {
	case PointsAwarded:
		return "+" + strconv.FormatInt(e.Points, 10)
	case PointsSpent, PointsClawedBack:
		return strconv.FormatInt(e.Points, 10)
	default:
		panic("pages: unknown points entry kind: " + string(e.Kind))
	}
}

// What describes the line in the chrome language.
func (e PointsEntry) What(ctx context.Context) string {
	switch e.Kind {
	case PointsAwarded:
		if e.Order != "" {
			return fmt.Sprintf(i18n.T(ctx, i18n.KeyPointsFromOrder), e.Order)
		}
		return i18n.T(ctx, i18n.KeyPointsEarned)
	case PointsSpent:
		return i18n.T(ctx, i18n.KeyPointsSpent)
	case PointsClawedBack:
		return i18n.T(ctx, i18n.KeyPointsClawback)
	default:
		panic("pages: unknown points entry kind: " + string(e.Kind))
	}
}

// Detail makes a clawback's requested amount and any uncollected shortfall
// visible. A zero-point row is otherwise indistinguishable from a blank event.
func (e PointsEntry) Detail(ctx context.Context) string {
	switch e.Kind {
	case PointsAwarded, PointsSpent:
		return ""
	case PointsClawedBack:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyPointsClawbackDetail),
			strconv.FormatInt(e.RequestedPoints, 10),
			strconv.FormatInt(-e.Points, 10),
			strconv.FormatInt(e.ShortfallPoints, 10))
	default:
		panic("pages: unknown points entry kind: " + string(e.Kind))
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
	// OperationID identifies one rendered redemption form across HTTP retries.
	OperationID string
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
