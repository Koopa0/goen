package policy

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/widget"
)

// Rules is what the shop states about itself, from where each rule is stored.
// The home, department and product pages show it: as RulesSection, or as the
// stats Stats and its parts return.
type Rules struct {
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

// Rules reads the rules the shop states about itself. Where checkout offers
// no pickup (WithoutPickup), a threshold or floor does not count it.
func (s *Store) Rules(ctx context.Context) (Rules, error) {
	withPickup := !s.noPickup
	freeOver, err := s.q.FreeDeliveryThreshold(ctx, withPickup)
	if err != nil {
		return Rules{}, fmt.Errorf("read free delivery threshold: %w", err)
	}
	lowestFee, err := s.q.LowestDeliveryFee(ctx, withPickup)
	if err != nil {
		return Rules{}, fmt.Errorf("read lowest delivery fee: %w", err)
	}
	windows, err := s.q.StatedWindows(ctx)
	if err != nil {
		return Rules{}, fmt.Errorf("read stated windows: %w", err)
	}
	return Rules{
		FreeDeliveryCents: freeOver, LowestFeeCents: lowestFee, PickupOffered: withPickup,
		RescissionDays: windows.RescissionDays, ReturnDays: windows.ReturnDays,
		HoldMinutes: windows.HoldMinutes, PayStartMinutes: windows.PayStartMinutes,
	}, nil
}

func (r Rules) Stats(ctx context.Context) []widget.Stat {
	return []widget.Stat{
		r.HoldStat(ctx),
		r.RescissionStat(ctx),
		{
			Label: i18n.T(ctx, i18n.KeyRuleReturn),
			Value: widget.StatCount(r.ReturnDays, i18n.T(ctx, i18n.KeyRuleUnitDays)),
			Note:  i18n.T(ctx, i18n.KeyRuleReturnNote),
		},
		r.FreeDeliveryStat(ctx),
	}
}

func (r Rules) HoldStat(ctx context.Context) widget.Stat {
	return widget.Stat{
		Label: i18n.T(ctx, i18n.KeyRuleHold),
		Value: widget.StatCount(r.HoldMinutes, i18n.T(ctx, i18n.KeyRuleUnitMinutes)),
		Note:  fmt.Sprintf(i18n.T(ctx, i18n.KeyRuleHoldNote), strconv.FormatInt(r.PayStartMinutes, 10)),
	}
}

func (r Rules) RescissionStat(ctx context.Context) widget.Stat {
	return widget.Stat{
		Label: i18n.T(ctx, i18n.KeyRuleRescission),
		Value: widget.StatCount(r.RescissionDays, i18n.T(ctx, i18n.KeyRuleUnitDays)),
		Note:  i18n.T(ctx, i18n.KeyRuleRescissionNote),
	}
}

// FreeDeliveryStat has no figure where some delivery method never turns free, and a stat with no figure is left out.
func (r Rules) FreeDeliveryStat(ctx context.Context) widget.Stat {
	if r.FreeDeliveryCents <= 0 {
		return widget.Stat{}
	}
	return widget.Stat{
		Label: i18n.T(ctx, i18n.KeyRuleFreeDelivery),
		Value: widget.StatMoney(r.FreeDeliveryCents),
		Note:  fmt.Sprintf(i18n.T(ctx, r.freeDeliveryNoteKey()), money.TWD(r.LowestFeeCents)),
	}
}

func (r Rules) freeDeliveryNoteKey() i18n.Key {
	if r.PickupOffered {
		return i18n.KeyRuleFreeDeliveryNote
	}
	return i18n.KeyRuleFreeDeliveryHomeNote
}
