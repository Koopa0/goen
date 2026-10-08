package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
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
	CreditCents     int64
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
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyPointsSpent), money.TWD(e.CreditCents))
	case PointsClawedBack:
		if e.Order != "" {
			return fmt.Sprintf(i18n.T(ctx, i18n.KeyPointsClawbackOrder), e.Order)
		}
		return i18n.T(ctx, i18n.KeyPointsClawback)
	default:
		panic("pages: unknown points entry kind: " + string(e.Kind))
	}
}

func (e PointsEntry) Detail(ctx context.Context) string {
	switch e.Kind {
	case PointsAwarded, PointsSpent:
		return ""
	case PointsClawedBack:
		if e.ShortfallPoints > 0 {
			return fmt.Sprintf(i18n.T(ctx, i18n.KeyPointsClawbackDetail), strconv.FormatInt(e.ShortfallPoints, 10))
		}
		return ""
	default:
		panic("pages: unknown points entry kind: " + string(e.Kind))
	}
}

type PointsView struct {
	// Bound pages the ledger; the balance below covers all of it.
	web.Bound

	TierName     string
	MultiplierBP int32
	DraftPoints  *string
	FieldError   string

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

func (v *PointsView) BalanceText() string { return strconv.FormatInt(v.Balance, 10) }

func (v *PointsView) RedeemableText() string { return strconv.FormatInt(v.Redeemable, 10) }

func (v *PointsView) Credit() string { return twd(v.CreditCents) }

func (v *PointsView) CanRedeem() bool { return v.Redeemable > 0 }

func (v *PointsView) ShowRedemptionForm() bool { return v.CanRedeem() || v.DraftPoints != nil }

func (v *PointsView) MinimumText() string { return strconv.FormatInt(v.Minimum, 10) }

func (v *PointsView) RateText(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyPointsRate), strconv.FormatInt(v.PerCredit, 10))
}

func (v *PointsView) Expiring() bool { return v.ExpiringPoints > 0 && v.ExpiringOn != "" }

func (v *PointsView) ExpiringText(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyPointsExpiring),
		strconv.FormatInt(v.ExpiringPoints, 10), v.ExpiringOn)
}

func (v *PointsView) Empty() bool { return len(v.Entries) == 0 }

func (v *PointsView) StepText() string { return strconv.FormatInt(v.PerCredit, 10) }

func (v *PointsView) PointsValue() string {
	if v.DraftPoints != nil {
		return *v.DraftPoints
	}
	return v.RedeemableText()
}

func (v *PointsView) PointsDescribedBy() string {
	if v.FieldError != "" {
		return "points-rule points-error"
	}
	return "points-rule"
}

func (v *PointsView) pointsFieldAttrs() templ.Attributes {
	attrs := templ.Attributes{
		"inputmode": "numeric", "max": v.RedeemableText(), "min": v.MinimumText(), "required": true, "step": v.StepText(),
	}
	if v.FieldError == "" {
		attrs["aria-describedby"] = "points-rule"
	}
	return attrs
}

func (v *PointsView) Multiplier(ctx context.Context) string {
	return (MemberStanding{MultiplierBP: v.MultiplierBP}).Multiplier(ctx)
}

func (v *PointsView) MemberRate(ctx context.Context) string {
	rate := v.Multiplier(ctx)
	if v.TierName != "" {
		return v.TierName + " " + rate
	}
	return rate
}
