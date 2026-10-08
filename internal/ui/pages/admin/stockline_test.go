package admin

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/inventory"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// stockDays is len(levels) shop days ending with the last level; each receipt
// is a day received[i] units arrived on, as one movement.
func stockDays(levels []int32, received map[int]int32) []StockDay {
	out := make([]StockDay, len(levels))
	for i, level := range levels {
		out[i] = StockDay{Day: time.Date(2026, 9, 1+i, 0, 0, 0, 0, time.UTC), Stock: level}
		if units := received[i]; units > 0 {
			out[i].Received, out[i].Receipts, out[i].Moves = units, 1, 1
		}
	}
	return out
}

func TestStockLineCaptionCountsEachSentenceOnItsOwn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		view       MovementsView
		en, zhHant string
	}{
		{
			"one receipt, one day at the safety stock",
			MovementsView{Stock: 6, Safety: 2, Days: stockDays([]int32{9, 2, 36, 6}, map[int]int32{2: 34})},
			"6 in stock; received 1 time in 4 days, at or below the safety stock on 1 day.",
			"目前 6 件；4 天裡進貨 1 次，有 1 天不高於安全庫存。",
		},
		{
			"two receipts, several days at the safety stock",
			MovementsView{Stock: 5, Safety: 2, Days: stockDays([]int32{2, 2, 30, 5}, map[int]int32{0: 2, 2: 28})},
			"5 in stock; received 2 times in 4 days, at or below the safety stock on 2 days.",
			"目前 5 件；4 天裡進貨 2 次，有 2 天不高於安全庫存。",
		},
		{
			"days below the safety stock count too",
			MovementsView{Stock: 5, Safety: 2, Days: stockDays([]int32{0, 0, 1, 5}, map[int]int32{3: 5})},
			"5 in stock; received 1 time in 4 days, at or below the safety stock on 3 days.",
			"目前 5 件；4 天裡進貨 1 次，有 3 天不高於安全庫存。",
		},
		{
			"never at the safety stock",
			MovementsView{Stock: 8, Safety: 2, Days: stockDays([]int32{12, 10, 8}, nil)},
			"8 in stock; received 0 times in 3 days, never below 8.",
			"目前 8 件；3 天裡進貨 0 次，最低到過 8 件。",
		},
	} {
		for _, loc := range []struct {
			locale i18n.Locale
			want   string
		}{{i18n.En, tc.en}, {i18n.ZhHant, tc.zhHant}} {
			ctx := i18n.WithLocale(t.Context(), loc.locale)
			if got := tc.view.StockLine(ctx).Caption; got != loc.want {
				t.Errorf("%s, %s: StockLine().Caption = %q, want %q", tc.name, loc.locale, got, loc.want)
			}
		}
	}
}

func TestStockLineIsDrawnFromTwoMovementsAndSaidInOneSentenceBelow(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		moves int32
		draws bool
		en    string
	}{
		{"none", 0, false, "No stock movement in the last 3 days."},
		{"one", 1, false, "Only one stock movement in the last 3 days, too few to draw a line."},
		{"two", 2, true, ""},
	} {
		v := MovementsView{Safety: 2, Days: stockDays([]int32{5, 5, 5}, nil)}
		v.Days[1].Moves = tc.moves
		if got := v.DrawsStockLine(); got != tc.draws {
			t.Errorf("%s: DrawsStockLine() = %v, want %v", tc.name, got, tc.draws)
		}
		if tc.draws {
			continue
		}
		if got := v.NoStockLine(i18n.WithLocale(t.Context(), i18n.En)); got != tc.en {
			t.Errorf("%s: NoStockLine() = %q, want %q", tc.name, got, tc.en)
		}
	}
}

func TestMovementsPageDrawsTheStockLineOnlyWhenTheDaysWereRead(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := func(days []StockDay) string {
		return renderComponent(t, ctx, Movements(layouts.Page{Title: "Stock"}, &MovementsView{
			SKU: "SKU-1", ProductName: "Thing", Slug: "thing", Stock: 6, Safety: 2, FormID: "f", Days: days, Rows: []Movement{{Delta: 2, Reason: inventory.ReasonReceipt}},
		}))
	}
	if got := view(stockDays([]int32{2, 2, 36, 6}, map[int]int32{0: 2, 2: 34})); !strings.Contains(got, `class="goen-chart"`) {
		t.Error("the movements page of a variant with movements draws no stock line")
	}
	if got := view(nil); strings.Contains(got, `class="goen-chart"`) {
		t.Error("a later page of the ledger, which reads no days, draws a stock line")
	}
}
