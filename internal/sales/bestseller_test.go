package sales

import (
	"fmt"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestBestSeller_FactsBrand: best seller facts name the brand then what sold.
func TestBestSeller_FactsBrand(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	got := BestSeller{Brand: "Aurora", Units: 4, RevenueCents: 120000}.Facts(ctx)
	if want := "Aurora · 4 units sold · Product gross NT$1,200"; got != want {
		t.Errorf("BestSeller.Facts = %q, want %q", got, want)
	}
	if got := (BestSeller{Units: 4, RevenueCents: 120000}).Facts(ctx); strings.HasPrefix(got, " ·") || strings.HasPrefix(got, "·") {
		t.Errorf("BestSeller.Facts without a brand = %q, want no leading separator", got)
	}
}

// TestBestSeller_FactsUnits: best seller facts count units in both languages.
func TestBestSeller_FactsUnits(t *testing.T) {
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
			got := (BestSeller{Units: count}).Facts(i18n.WithLocale(t.Context(), locale))
			if !strings.Contains(got, want) {
				t.Errorf("Facts(%s, %d) = %q, want %q", locale, count, got, want)
			}
		}
	}
}

// TestBestSeller_Href: a best seller links to its product in the back office.
func TestBestSeller_Href(t *testing.T) {
	t.Parallel()

	if got, want := (BestSeller{Slug: "aurora-lamp"}).Href(), "/admin/products/aurora-lamp"; got != want {
		t.Errorf("Href = %q, want %q", got, want)
	}
}

// TestTopUnits: the scale of a best seller list is its largest sale wherever
// that sits in the list.
func TestTopUnits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		sellers []BestSeller
		want    int64
	}{
		{"none", nil, 0},
		{"one", []BestSeller{{Units: 4}}, 4},
		{"largest first", []BestSeller{{Units: 9}, {Units: 3}}, 9},
		{"largest last", []BestSeller{{Units: 3}, {Units: 9}}, 9},
	}
	for _, tt := range tests {
		if got := TopUnits(tt.sellers); got != tt.want {
			t.Errorf("TopUnits(%s) = %d, want %d", tt.name, got, tt.want)
		}
	}
}
