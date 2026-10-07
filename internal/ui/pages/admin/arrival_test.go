package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestStockArrivalIsAnOptionalPlainFormAndPreservesARefusedDate(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			view := VariantsView{Return: "/admin/stock?q=A", Variants: []Variant{
				{SKU: "A", ArrivalInput: "2026-02-30", ArrivalError: i18n.T(ctx, i18n.KeyAdminVariantArrivalError)},
				{SKU: "B", ArrivalInput: "2026-10-15"},
			}}
			html := renderComponent(t, ctx, Variants(layouts.Page{}, view))
			for _, want := range []string{i18n.T(ctx, i18n.KeyAdminVariantArrivalSave), `method="post" action="/admin/stock/arrival"`, `type="date" name="arrival_on" value="2026-02-30"`, `for="arrival-A"`, `aria-describedby="arrival-error-A"`, `id="arrival-error-A"`, `value="2026-10-15"`} {
				if !strings.Contains(html, want) {
					t.Errorf("form missing %q", want)
				}
			}
			if strings.Contains(html, `arrival-error-B`) {
				t.Error("unmodified row marked invalid")
			}
		})
	}
}

func TestStockDeskNamesTheVariantListAfterTheCoverSection(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			view := VariantsView{ShowCover: true, Variants: []Variant{{SKU: "A"}}}
			html := renderComponent(t, ctx, Variants(layouts.Page{}, view))
			order := []string{
				`<h2 class="goen-admin__subhead" id="stock">` + i18n.T(ctx, i18n.KeyAdminRepStock),
				`<section aria-labelledby="stock-list-heading">`,
				`<h2 class="goen-sr-only" id="stock-list-heading">` + i18n.T(ctx, i18n.KeyAdminStockList) + `</h2>`,
				`<table`,
			}
			from := 0
			for _, want := range order {
				i := strings.Index(html[from:], want)
				if i < 0 {
					t.Fatalf("%q missing or out of order after offset %d", want, from)
				}
				from += i + len(want)
			}
		})
	}
}
