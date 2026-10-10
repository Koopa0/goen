package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

// PointsEntryKind is closed by loyalty_entries_kind_shape. Declared here because
// loyalty builds these view models; a type it owned could not be named below
// without a cycle.
type PointsEntryKind string

const (
	PointsAwarded    PointsEntryKind = "award"
	PointsSpent      PointsEntryKind = "spend"
	PointsClawedBack PointsEntryKind = "clawback"
)

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

// Detail exists because a zero-point row is otherwise indistinguishable from a blank event.
func (e PointsEntry) Detail(ctx context.Context) string {
	switch e.Kind {
	case PointsAwarded, PointsSpent:
		return ""
	case PointsClawedBack:
		return i18n.Count(ctx, i18n.KeyPointsClawbackDetail, e.RequestedPoints,
			strconv.FormatInt(e.RequestedPoints, 10),
			strconv.FormatInt(-e.Points, 10),
			strconv.FormatInt(e.ShortfallPoints, 10))
	default:
		panic("pages: unknown points entry kind: " + string(e.Kind))
	}
}

type PointsView struct {
	// Bound pages the ledger; the balance below covers all of it.
	web.Bound

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

func (v PointsView) BalanceText() string { return strconv.FormatInt(v.Balance, 10) }

func (v PointsView) RedeemableText() string { return strconv.FormatInt(v.Redeemable, 10) }

func (v PointsView) Credit() string { return twd(v.CreditCents) }

func (v PointsView) CanRedeem() bool { return v.Redeemable > 0 }

func (v PointsView) MinimumText() string { return strconv.FormatInt(v.Minimum, 10) }

func (v PointsView) RateText(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyPointsRate, v.PerCredit, strconv.FormatInt(v.PerCredit, 10))
}

func (v PointsView) StepAmountText(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyPointsAmount, v.PerCredit, v.StepText())
}

func (v PointsView) Expiring() bool { return v.ExpiringPoints > 0 && v.ExpiringOn != "" }

func (v PointsView) ExpiringText(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyPointsExpiring, v.ExpiringPoints,
		strconv.FormatInt(v.ExpiringPoints, 10), v.ExpiringOn)
}

func (v PointsView) Empty() bool { return len(v.Entries) == 0 }

func (v PointsView) StepText() string { return strconv.FormatInt(v.PerCredit, 10) }
