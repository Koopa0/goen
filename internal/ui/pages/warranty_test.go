package pages

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestWarrantyCopyWaitsForDelivery renders every customer-facing empty/refusal
// path in both supported locales. Dispatch is not the eligibility boundary;
// delivery is.
func TestWarrantyCopyWaitsForDelivery(t *testing.T) {
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   []string
		old    []string
	}{
		{
			name: "Traditional Chinese", locale: i18n.ZhHant,
			want: []string{"送達之後,到", "送達之後就可以登錄。", "可能還沒送達"},
			old:  []string{"出貨之後,到", "出貨之後就可以登錄。", "可能還沒出貨"},
		},
		{
			name: "English", locale: i18n.En,
			want: []string{
				"Once an order has been delivered, open it from ",
				"Registration opens on delivery.",
				"may not have been delivered",
			},
			old: []string{
				"Once an order has shipped", "Registration opens once it ships",
				"may not have shipped",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			list := renderWarrantyInLocale(t, ctx, WarrantyList(
				layouts.Page{Title: "Warranty"}, WarrantyListView{},
			))
			order := renderWarrantyInLocale(t, ctx, WarrantyOrder(
				layouts.Page{Title: "Warranty"}, WarrantyOrderView{
					Number: "GOEN-TEST",
					Notice: i18n.T(ctx, i18n.KeyWarrantyRefused),
					Lines: []WarrantyLine{{
						ID: "line-1", Name: "Item", Months: 12, HasTerm: true,
					}},
				},
			))
			html := list + order
			for _, want := range tt.want {
				if !strings.Contains(html, want) {
					t.Errorf("rendered warranty copy does not contain %q", want)
				}
			}
			for _, old := range tt.old {
				if strings.Contains(html, old) {
					t.Errorf("rendered warranty copy still says %q", old)
				}
			}
		})
	}
}

func renderWarrantyInLocale(t *testing.T, ctx context.Context, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	// context.Context is kept at the call site so locale selection is explicit.
	if err := c.Render(ctx, &b); err != nil {
		t.Fatalf("render warranty: %v", err)
	}
	return b.String()
}
