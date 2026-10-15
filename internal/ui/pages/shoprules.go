package pages

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
)

// ShopRules is what the shop states about itself, from where each rule is stored.
type ShopRules struct {
	// FreeDeliveryCents is zero where some delivery method never turns free.
	FreeDeliveryCents int64
	LowestFeeCents    int64
	PickupOffered     bool
	// RescissionDays is the statutory window and ReturnDays goen's own, in days from the day after delivery.
	RescissionDays int64
	ReturnDays     int64
	// HoldMinutes is how long placing an order holds the stock, and PayStartMinutes how long the customer then
	// has to start paying.
	HoldMinutes     int64
	PayStartMinutes int64
}

func (r ShopRules) Stats(ctx context.Context) []components.Stat {
	return []components.Stat{
		r.holdStat(ctx),
		r.rescissionStat(ctx),
		{
			Label: i18n.T(ctx, i18n.KeyRuleReturn),
			Value: components.StatCount(r.ReturnDays, i18n.T(ctx, i18n.KeyRuleUnitDays)),
			Note:  i18n.T(ctx, i18n.KeyRuleReturnNote),
		},
		r.freeDeliveryStat(ctx),
	}
}

// countUnit is the words of a counted message after its number, so a stat can set the figure and the unit apart.
func countUnit(ctx context.Context, k i18n.Key, n int64) string {
	return strings.TrimSpace(strings.TrimLeft(i18n.Count(ctx, k, n, n), "0123456789"))
}

func (r ShopRules) holdStat(ctx context.Context) components.Stat {
	return components.Stat{
		Label: i18n.T(ctx, i18n.KeyRuleHold),
		Value: components.StatCount(r.HoldMinutes, i18n.T(ctx, i18n.KeyRuleUnitMinutes)),
		Note:  fmt.Sprintf(i18n.T(ctx, i18n.KeyRuleHoldNote), strconv.FormatInt(r.PayStartMinutes, 10)),
	}
}

func (r ShopRules) rescissionStat(ctx context.Context) components.Stat {
	return components.Stat{
		Label: i18n.T(ctx, i18n.KeyRuleRescission),
		Value: components.StatCount(r.RescissionDays, i18n.T(ctx, i18n.KeyRuleUnitDays)),
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
