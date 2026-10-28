package sales

import (
	"fmt"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestSeller_FactsBrand: seller facts name the brand then what sold.
func TestSeller_FactsBrand(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	got := Seller{Brand: "Aurora", Units: 4, RevenueCents: 120000}.Facts(ctx)
	if want := "Aurora · 4 units sold · Product gross NT$1,200"; got != want {
		t.Errorf("Seller.Facts = %q, want %q", got, want)
	}
	if got := (Seller{Units: 4, RevenueCents: 120000}).Facts(ctx); strings.HasPrefix(got, " ·") || strings.HasPrefix(got, "·") {
		t.Errorf("Seller.Facts without a brand = %q, want no leading separator", got)
	}
}

// TestSeller_FactsUnits: seller facts count units in both languages.
func TestSeller_FactsUnits(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, count := range []int64{0, 1, 2, 1000} {
			want := fmt.Sprintf("%d units sold", count)
			if count == 1 {
				want = "1 unit sold"
			}
			if locale == i18n.ZhHant {
				want = fmt.Sprintf("售出 %d 件", count)
			}
			got := (Seller{Units: count}).Facts(i18n.WithLocale(t.Context(), locale))
			if !strings.Contains(got, want) {
				t.Errorf("Facts(%s, %d) = %q, want %q", locale, count, got, want)
			}
		}
	}
}

// TestSeller_Href: a seller links to its product in the back office.
func TestSeller_Href(t *testing.T) {
	t.Parallel()

	if got, want := (Seller{Slug: "aurora-lamp"}).Href(), "/admin/products/aurora-lamp"; got != want {
		t.Errorf("Href = %q, want %q", got, want)
	}
}
