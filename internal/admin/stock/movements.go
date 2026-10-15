package stock

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/inventory"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/components"
)

type Movement struct {
	At          string
	Delta       int32
	Reason      inventory.MovementReason
	OrderNumber string
	Actor       string
	Running     int32
}

func (m Movement) DeltaText() string {
	if m.Delta > 0 {
		return "+" + strconv.FormatInt(int64(m.Delta), 10)
	}
	return strconv.FormatInt(int64(m.Delta), 10)
}

func (m Movement) RunningText() string { return strconv.FormatInt(int64(m.Running), 10) }

func (m Movement) In() bool { return m.Delta > 0 }

func (m Movement) HasOrder() bool { return m.OrderNumber != "" }

func (m Movement) By(ctx context.Context) string {
	if m.Actor == "" {
		return i18n.T(ctx, i18n.KeyAdminActorSystem)
	}
	return m.Actor
}

func (m Movement) ReasonText(ctx context.Context) string {
	switch m.Reason {
	case inventory.ReasonReceipt:
		return i18n.T(ctx, i18n.KeyAdminMoveReceipt)
	case inventory.ReasonHold:
		return i18n.T(ctx, i18n.KeyAdminMoveHold)
	case inventory.ReasonSale:
		return i18n.T(ctx, i18n.KeyAdminMoveSale)
	case inventory.ReasonRelease:
		return i18n.T(ctx, i18n.KeyAdminMoveRelease)
	case inventory.ReasonReturn:
		return i18n.T(ctx, i18n.KeyAdminMoveReturn)
	case inventory.ReasonAdjustment:
		return i18n.T(ctx, i18n.KeyAdminMoveAdjustment)
	default:
		panic("pages: no label for inventory movement reason " + string(m.Reason))
	}
}

type MovementsView struct {
	keyset.Bound

	SKU           string
	ProductName   string
	Slug          string
	Stock         int32
	Safety        int32
	Rows          []Movement
	Days          []StockDay
	Notice        components.Result
	FormID        string
	DraftQuantity string
	QuantityError string
	ReceiptKey    string
}

// ReceiveKey is the goods-receipt form's idempotency key, named by the rendered
// form for the reason AdjustKey is. Its prefix differs from AdjustKey's so the
// two forms never share a key.
func (v *MovementsView) ReceiveKey() string {
	if v.ReceiptKey != "" {
		return v.ReceiptKey
	}
	return "rcv:" + v.SKU + ":" + v.FormID
}

func (v *MovementsView) Empty() bool { return len(v.Rows) == 0 }

func (v *MovementsView) StockText() string { return strconv.FormatInt(int64(v.Stock), 10) }

func (v *MovementsView) SafetyText() string { return strconv.FormatInt(int64(v.Safety), 10) }
