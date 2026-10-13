package email

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
)

// PlacedSnapshot records the terms when checkout commits, rather than reading
// a changed order or catalogue when the outbox finally sends its confirmation.
type PlacedSnapshot struct {
	Lines          []PlacedLine `json:"lines"`
	SubtotalCents  int64        `json:"subtotal_cents"`
	ShippingCents  int64        `json:"shipping_cents"`
	DiscountCents  int64        `json:"discount_cents"`
	DiscountReason string       `json:"discount_reason"`
	TaxCents       int64        `json:"tax_cents"`
	CreditCents    int64        `json:"credit_cents"`
	ShippingName   string       `json:"shipping_name"`
	DeliveryTo     string       `json:"delivery_to"`
	Phone          string       `json:"phone"`
	DeliveryNote   string       `json:"delivery_note"`
	HoldUntil      time.Time    `json:"hold_until"`
	StartBy        time.Time    `json:"start_by"`
}

type PlacedLine struct {
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	Label     string `json:"label"`
	UnitCents int64  `json:"unit_cents"`
	Quantity  int32  `json:"quantity"`
}

func (n Notifier) placedConfirmation(ctx context.Context, p *OrderPlaced, owed int64, now time.Time) string {
	s := p.Snapshot
	blocks := []string{fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedReceived), p.OrderNumber)}
	lines := make([]string, 1, 1+2*len(s.Lines))
	lines[0] = i18n.T(ctx, i18n.KeyMailPlacedLines)
	for _, line := range s.Lines {
		name := line.Name
		if line.Label != "" {
			name += " · " + line.Label
		}
		lines = append(lines, name, fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedLine),
			line.SKU, strconv.FormatInt(int64(line.Quantity), 10), twd(line.UnitCents), twd(line.UnitCents*int64(line.Quantity))))
	}
	blocks = append(blocks, strings.Join(lines, "\n"))
	shipping := twd(s.ShippingCents)
	if s.ShippingCents == 0 {
		shipping = i18n.T(ctx, i18n.KeyFreeShipping)
	}
	amounts := make([]string, 0, 6)
	amounts = append(amounts, i18n.T(ctx, i18n.KeyMailPlacedTotals),
		fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedFact), i18n.T(ctx, i18n.KeySubtotal), twd(s.SubtotalCents)),
		fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedFact), fmt.Sprintf(i18n.T(ctx, i18n.KeyOrderShippingFee), s.ShippingName), shipping))
	if s.DiscountCents > 0 {
		label := i18n.T(ctx, i18n.KeyDiscount)
		if s.DiscountReason != "" {
			label = fmt.Sprintf(i18n.T(ctx, i18n.KeyDiscountFor), s.DiscountReason)
		}
		amounts = append(amounts, fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedFact), label, "-"+twd(s.DiscountCents)))
	}
	if s.CreditCents > 0 {
		amounts = append(amounts,
			fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedFact), i18n.T(ctx, i18n.KeyOrderCreditApplied), "-"+twd(s.CreditCents)),
			fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedFact), i18n.T(ctx, i18n.KeyOrderAmountDue), twd(owed)))
	} else {
		amounts = append(amounts, fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedFact), i18n.T(ctx, i18n.KeyOrderGrandTotal), twd(p.TotalCents)))
	}
	blocks = append(blocks, strings.Join(amounts, "\n"), strings.Join([]string{
		i18n.T(ctx, i18n.KeyMailPlacedDelivery), s.ShippingName,
		fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedFact), i18n.T(ctx, i18n.KeyFieldRecipient), p.Name),
		fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedFact), i18n.T(ctx, i18n.KeyFieldPhoneShort), s.Phone),
		s.DeliveryTo,
	}, "\n"))
	if s.DeliveryNote != "" {
		blocks = append(blocks, fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedFact), i18n.T(ctx, i18n.KeyMailPlacedNote), s.DeliveryNote))
	}
	var funding string
	switch {
	case owed > 0 && s.CreditCents > 0:
		funding = fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedMixed), twd(s.CreditCents), twd(owed))
	case owed > 0:
		funding = fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedCard), twd(owed))
	case s.CreditCents > 0:
		funding = fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedCredit), twd(s.CreditCents))
	default:
		funding = i18n.T(ctx, i18n.KeyMailPlacedZero)
	}
	blocks = append(blocks, i18n.T(ctx, i18n.KeyMailPlacedFunding)+"\n"+funding)
	if owed > 0 {
		key := i18n.KeyMailPlacedDeadline
		if now.After(s.StartBy) {
			key = i18n.KeyMailPlacedExpired
		}
		blocks = append(blocks, fmt.Sprintf(i18n.T(ctx, key), shoptime.Minute(s.StartBy), shoptime.Minute(s.HoldUntil)))
	}
	blocks = append(blocks, fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedCurrent), n.orderURL(p.OrderNumber)))
	return strings.Join(blocks, "\n\n")
}
