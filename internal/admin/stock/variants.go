package stock

import (
	"context"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/stockrisk"
	"github.com/koopa0/goen/internal/ui/widget"
)

type Variant struct {
	SKU                          string
	Slug                         string
	ProductName                  string
	Brand                        string
	PriceCents                   int64
	CompareCents                 int64
	Stock                        int32
	Safety                       int32
	Active                       bool
	Options                      []string
	FormID                       string
	DraftQuantity, QuantityError string
	ArrivalInput, ArrivalError   string
}

func (v *Variant) OptionText() string { return strings.Join(v.Options, " · ") }

func (v *Variant) StockText() string { return strconv.FormatInt(int64(v.Stock), 10) }

func (v *Variant) SafetyText() string { return strconv.FormatInt(int64(v.Safety), 10) }

func (v *Variant) SellableText() string {
	n := max(v.Stock-v.Safety, 0)
	return strconv.FormatInt(int64(n), 10)
}

func (v *Variant) Low() bool { return v.Stock <= v.Safety }

type VariantsView struct {
	keyset.Bound

	Variants    []Variant
	SoldOutOnly bool
	Term        string
	Notice      widget.Result
	// ShowCover is set on the desk's opening page only: not on a search, the
	// sold out filter or a later page of the list.
	ShowCover   bool
	AtRisk      []stockrisk.Row
	MoreSoldOut int
	// Return is this page's own address, filter and position, which each form
	// posts back so a write returns to the page it was made on.
	Return string
}

func (v VariantsView) AllHref() string { return keyset.ScopeURL("/admin/stock", "q", v.Term) }

func (v VariantsView) SoldOutHref() string {
	return keyset.ScopeURL("/admin/stock", "soldout", "1", "q", v.Term)
}

func (v VariantsView) Empty() bool { return len(v.Variants) == 0 }

func VariantsMeta(ctx context.Context) frame.Page {
	return frame.Page{Title: i18n.T(ctx, i18n.KeyAdminPageStockList)}
}

func (v *Variant) PriceText() string { return strconv.FormatInt(v.PriceCents/100, 10) }

func (v *Variant) CompareText() string {
	if v.CompareCents <= 0 {
		return ""
	}
	return strconv.FormatInt(v.CompareCents/100, 10)
}

// ReceiveKey is the row's goods-receipt idempotency key. It is spent for good
// in the ledger, so it names the rendered form and not the stock level: stock
// returns to an earlier figure, and a key built from it would then be refused.
func (v *Variant) ReceiveKey() string {
	return "rcv:" + v.SKU + ":" + v.FormID
}

func (v VariantsView) Refusals() []widget.Refusal {
	out := make([]widget.Refusal, 0, len(v.Variants))
	for i := range v.Variants {
		row := &v.Variants[i]
		out = append(out, widget.SummarizeRefusal(i18n.KeyAdminRefusalReceipt, map[string]string{"quantity": row.QuantityError}, widget.RefusalField{Key: "quantity", ID: "rcv-" + row.SKU}))
	}
	return out
}
