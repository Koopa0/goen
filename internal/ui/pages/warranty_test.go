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

// TestWarrantyOrderEmptyHintMatchesWhy locks the empty banner to the same
// reasons Why() already names. The delivery sentence is only true when every
// line is still waiting to arrive.
func TestWarrantyOrderEmptyHintMatchesWhy(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	afterShipping := i18n.T(ctx, i18n.KeyWarrantyAfterShipping)
	allDone := i18n.T(ctx, i18n.KeyWarrantyAllDone)
	noTerm := i18n.T(ctx, i18n.KeyWarrantyNoTerm)

	tests := []struct {
		name    string
		lines   []WarrantyLine
		want    string
		notWant string
	}{
		{
			name: "every unit already registered",
			lines: []WarrantyLine{{
				ID: "line-1", Name: "Item", Months: 12, HasTerm: true,
				Delivered: 1, Registered: 1,
			}},
			want:    allDone,
			notWant: afterShipping,
		},
		{
			name: "no warranty term",
			lines: []WarrantyLine{{
				ID: "line-1", Name: "Item", HasTerm: false,
			}},
			want:    noTerm,
			notWant: afterShipping,
		},
		{
			name: "mixed undelivered and registered",
			lines: []WarrantyLine{
				{
					ID: "line-1", Name: "Waiting", Months: 12, HasTerm: true,
				},
				{
					ID: "line-2", Name: "Done", Months: 12, HasTerm: true,
					Delivered: 1, Registered: 1,
				},
			},
			want:    "",
			notWant: afterShipping,
		},
		{
			name: "every line still undelivered",
			lines: []WarrantyLine{{
				ID: "line-1", Name: "Item", Months: 12, HasTerm: true,
			}},
			want: afterShipping,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			html := renderWarrantyInLocale(t, ctx, WarrantyOrder(
				layouts.Page{Title: "Warranty"}, WarrantyOrderView{
					Number: "GOEN-TEST",
					Lines:  tt.lines,
				},
			))
			got := warrantyEmptyDesc(html)
			if got != tt.want {
				t.Errorf("empty hint = %q, want %q", got, tt.want)
			}
			if tt.notWant != "" && got == tt.notWant {
				t.Errorf("empty hint still uses the delivery sentence %q", tt.notWant)
			}
		})
	}
}

func warrantyEmptyDesc(html string) string {
	const mark = `class="ui-empty__desc"`
	i := strings.Index(html, mark)
	if i < 0 {
		return ""
	}
	rest := html[i:]
	gt := strings.Index(rest, ">")
	end := strings.Index(rest, "</p>")
	if gt < 0 || end < 0 || end <= gt {
		return ""
	}
	return rest[gt+1 : end]
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
