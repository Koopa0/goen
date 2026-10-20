package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
)

// ShopRules is the view of the rules the shop states about itself, as the home,
// department and product pages show them. Its fields are site.ShopRules's, in
// the same order, so each page's package converts the value site reads and the
// compiler refuses the conversion the day the two differ.
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
		r.HoldStat(ctx),
		r.RescissionStat(ctx),
		{
			Label: i18n.T(ctx, i18n.KeyRuleReturn),
			Value: components.StatCount(r.ReturnDays, i18n.T(ctx, i18n.KeyRuleUnitDays)),
			Note:  i18n.T(ctx, i18n.KeyRuleReturnNote),
		},
		r.FreeDeliveryStat(ctx),
	}
}

func (r ShopRules) HoldStat(ctx context.Context) components.Stat {
	return components.Stat{
		Label: i18n.T(ctx, i18n.KeyRuleHold),
		Value: components.StatCount(r.HoldMinutes, i18n.T(ctx, i18n.KeyRuleUnitMinutes)),
		Note:  fmt.Sprintf(i18n.T(ctx, i18n.KeyRuleHoldNote), strconv.FormatInt(r.PayStartMinutes, 10)),
	}
}

func (r ShopRules) RescissionStat(ctx context.Context) components.Stat {
	return components.Stat{
		Label: i18n.T(ctx, i18n.KeyRuleRescission),
		Value: components.StatCount(r.RescissionDays, i18n.T(ctx, i18n.KeyRuleUnitDays)),
		Note:  i18n.T(ctx, i18n.KeyRuleRescissionNote),
	}
}

// FreeDeliveryStat has no figure where some delivery method never turns free, and a stat with no figure is left out.
func (r ShopRules) FreeDeliveryStat(ctx context.Context) components.Stat {
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
