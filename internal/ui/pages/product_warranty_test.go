package pages

import (
	"context"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestProductWarrantyRendersWithoutDescriptionOrSpecs holds the PDP detail
// gate apart from warranty. Description and specs are independently optional;
// nesting warranty inside their guard hid a stated term when both were absent.
func TestProductWarrantyRendersWithoutDescriptionOrSpecs(t *testing.T) {
	t.Parallel()

	const (
		warrantyHeading = `id="warranty-heading"`
		warrantyBlock   = `class="goen-pdp__warranty"`
		detailBlock     = `class="goen-pdp__detail"`
		descHeading     = `id="desc-heading"`
		specsHeading    = `id="specs-heading"`
	)

	base := ProductView{
		Name: "Pixelight 9 Pro", Brand: "Pixelight", Slug: "pixelight-9-pro",
		SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
		PriceCents: 3690000,
	}

	tests := []struct {
		name         string
		mutate       func(*ProductView)
		locale       i18n.Locale
		wantDetail   bool
		wantWarranty bool
		wantText     func(context.Context, *ProductView) []string
	}{
		{
			name: "months only zh",
			mutate: func(v *ProductView) {
				v.WarrantyMonths = 24
			},
			locale:       i18n.ZhHant,
			wantDetail:   true,
			wantWarranty: true,
			wantText: func(ctx context.Context, v *ProductView) []string {
				return []string{v.WarrantyText(ctx)}
			},
		},
		{
			name: "months only en",
			mutate: func(v *ProductView) {
				v.WarrantyMonths = 24
			},
			locale:       i18n.En,
			wantDetail:   true,
			wantWarranty: true,
			wantText: func(ctx context.Context, v *ProductView) []string {
				return []string{v.WarrantyText(ctx)}
			},
		},
		{
			name: "note only zh",
			mutate: func(v *ProductView) {
				v.WarrantyNote = "原廠保固需上網登錄"
			},
			locale:       i18n.ZhHant,
			wantDetail:   true,
			wantWarranty: true,
			wantText: func(_ context.Context, v *ProductView) []string {
				return []string{v.WarrantyNote}
			},
		},
		{
			name: "note only en",
			mutate: func(v *ProductView) {
				v.WarrantyNote = "Register with the manufacturer"
			},
			locale:       i18n.En,
			wantDetail:   true,
			wantWarranty: true,
			wantText: func(_ context.Context, v *ProductView) []string {
				return []string{v.WarrantyNote}
			},
		},
		{
			name: "months and note zh",
			mutate: func(v *ProductView) {
				v.WarrantyMonths = 12
				v.WarrantyNote = "含電池一年"
			},
			locale:       i18n.ZhHant,
			wantDetail:   true,
			wantWarranty: true,
			wantText: func(ctx context.Context, v *ProductView) []string {
				return []string{v.WarrantyText(ctx), v.WarrantyNote}
			},
		},
		{
			name: "description and specs still render",
			mutate: func(v *ProductView) {
				v.Description = "旗艦手機"
				v.Specs = []ProductSpec{{Label: "重量", Value: "199 g"}}
			},
			locale:       i18n.ZhHant,
			wantDetail:   true,
			wantWarranty: false,
			wantText: func(_ context.Context, _ *ProductView) []string {
				return []string{"旗艦手機", "199 g"}
			},
		},
		{
			name:         "nothing stated",
			locale:       i18n.ZhHant,
			wantDetail:   false,
			wantWarranty: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := base
			if tt.mutate != nil {
				tt.mutate(&v)
			}
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			html := renderProductInLocale(t, ctx, &v)

			if got := strings.Contains(html, detailBlock); got != tt.wantDetail {
				t.Errorf("detail block present = %v, want %v", got, tt.wantDetail)
			}
			if got := strings.Contains(html, warrantyHeading); got != tt.wantWarranty {
				t.Errorf("warranty heading present = %v, want %v", got, tt.wantWarranty)
			}
			if got := strings.Contains(html, warrantyBlock); got != tt.wantWarranty {
				t.Errorf("warranty body present = %v, want %v", got, tt.wantWarranty)
			}
			if tt.wantText != nil {
				for _, want := range tt.wantText(ctx, &v) {
					if !strings.Contains(html, want) {
						t.Errorf("rendered HTML does not contain %q", want)
					}
				}
			}
			if !tt.wantWarranty {
				if strings.Contains(html, warrantyHeading) || strings.Contains(html, warrantyBlock) {
					t.Error("rendered an empty warranty section")
				}
			}
			if v.Description == "" && strings.Contains(html, descHeading) {
				t.Error("rendered a description section without copy")
			}
			if !v.HasSpecs() && strings.Contains(html, specsHeading) {
				t.Error("rendered a specs section without rows")
			}
		})
	}
}

func renderProductInLocale(t *testing.T, ctx context.Context, v *ProductView) string {
	t.Helper()
	var b strings.Builder
	if err := Product(ProductMeta(v), v).Render(ctx, &b); err != nil {
		t.Fatalf("render product: %v", err)
	}
	return b.String()
}
