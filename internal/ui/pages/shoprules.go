package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
)

// The statutory window and goen's own extension, in days from the day after delivery.
// A cart-package test binds both to return_window_ends and return_line_policy_window.
const (
	rescissionDays = 7
	returnDays     = 14
)

func RescissionDaysText() string { return strconv.Itoa(rescissionDays) }

func ReturnDaysText() string { return strconv.Itoa(returnDays) }

// ShopRules is what the shop states about itself, from where each rule is stored.
type ShopRules struct {
	// FreeDeliveryCents is zero where some delivery method never turns free.
	FreeDeliveryCents int64
	LowestFeeCents    int64
	PickupOffered     bool
}

func (r ShopRules) Stats(ctx context.Context) []components.Stat {
	stats := []components.Stat{
		r.holdStat(ctx),
		r.rescissionStat(ctx),
		{
			Label: i18n.T(ctx, i18n.KeyRuleReturn),
			Value: components.StatCount(returnDays, i18n.T(ctx, i18n.KeyRuleUnitDays)),
			Note:  i18n.T(ctx, i18n.KeyRuleReturnNote),
		},
	}
	return append(stats, r.freeDeliveryStat(ctx))
}

func (r ShopRules) holdStat(ctx context.Context) components.Stat {
	return components.Stat{
		Label: i18n.T(ctx, i18n.KeyRuleHold),
		Value: components.StatCount(holdMinutes, i18n.T(ctx, i18n.KeyRuleUnitMinutes)),
		Note:  fmt.Sprintf(i18n.T(ctx, i18n.KeyRuleHoldNote), PayStartMinutesText()),
	}
}

func (r ShopRules) rescissionStat(ctx context.Context) components.Stat {
	return components.Stat{
		Label: i18n.T(ctx, i18n.KeyRuleRescission),
		Value: components.StatCount(rescissionDays, i18n.T(ctx, i18n.KeyRuleUnitDays)),
		Note:  i18n.T(ctx, i18n.KeyRuleRescissionNote),
	}
}

// freeDeliveryStat has no figure where some delivery method never turns free, and a stat with no figure is left out.
func (r ShopRules) freeDeliveryStat(ctx context.Context) components.Stat {
	if r.FreeDeliveryCents <= 0 {
		return components.Stat{}
	}
	return components.Stat{
		Label: i18n.T(ctx, i18n.KeyRuleFreeDelivery),
		Value: components.StatMoney(r.FreeDeliveryCents),
		Note:  fmt.Sprintf(i18n.T(ctx, r.freeDeliveryNoteKey()), twd(r.LowestFeeCents)),
	}
}

func (r ShopRules) freeDeliveryNoteKey() i18n.Key {
	if r.PickupOffered {
		return i18n.KeyRuleFreeDeliveryNote
	}
	return i18n.KeyRuleFreeDeliveryHomeNote
}
