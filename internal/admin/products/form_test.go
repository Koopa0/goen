package products

import (
	"cmp"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/web"
)

func TestAFormNumberKeepsInvalidDistinctFromZero(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		raw  string
		max  int32
		want int32
		ok   bool
	}{
		{name: "blank is unstated", raw: "", max: MaxWarrantyMonths, ok: true},
		{name: "space is unstated", raw: "  ", max: MaxWarrantyMonths, ok: true},
		{name: "typed zero", raw: "0", max: MaxWarrantyMonths, ok: true},
		{name: "ordinary warranty", raw: "24", max: MaxWarrantyMonths, want: 24, ok: true},
		{name: "warranty ceiling", raw: "120", max: MaxWarrantyMonths, want: 120, ok: true},
		{name: "warranty over ceiling", raw: "121", max: MaxWarrantyMonths},
		{name: "letter typo", raw: "12o", max: MaxWarrantyMonths},
		{name: "decimal", raw: "24.0", max: MaxWarrantyMonths},
		{name: "negative", raw: "-3", max: MaxWarrantyMonths},
		{name: "inner space", raw: "2 4", max: MaxWarrantyMonths},
		{name: "non ASCII digits", raw: "١٢", max: MaxWarrantyMonths},
		{name: "longest ceiling", raw: "5000", max: carrier.MaxParcelLongestMM, want: 5000, ok: true},
		{name: "longest over ceiling", raw: "6000", max: carrier.MaxParcelLongestMM},
		{name: "sum ceiling", raw: "15000", max: carrier.MaxParcelSumMM, want: 15000, ok: true},
		{name: "weight ceiling", raw: "200000", max: carrier.MaxParcelWeightG, want: 200000, ok: true},
		{name: "comma", raw: "10,000", max: carrier.MaxParcelWeightG},
		{name: "safety ceiling", raw: "1000000", max: safetyStockCeiling, want: 1000000, ok: true},
		{name: "safety over ceiling", raw: "1000001", max: safetyStockCeiling},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := web.ParseBounded(tt.raw, tt.max)
			if got != tt.want || ok != tt.ok {
				t.Errorf("web.ParseBounded(%q, %d) = (%d, %v), want (%d, %v)",
					tt.raw, tt.max, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func FuzzParseBoundedInt(f *testing.F) {
	for _, seed := range []string{"", "0", "24", "12o", "-1", "10,000", "١٢"} {
		f.Add(seed, uint8(0))
	}
	f.Fuzz(func(t *testing.T, raw string, choice uint8) {
		maxima := [...]int32{
			MaxWarrantyMonths, carrier.MaxParcelLongestMM, carrier.MaxParcelSumMM,
			carrier.MaxParcelWeightG, safetyStockCeiling,
		}
		ceiling := maxima[int(choice)%len(maxima)]
		got, ok := web.ParseBounded(raw, ceiling)
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			if got != 0 || !ok {
				t.Fatalf("web.ParseBounded(%q, %d) = (%d, %v), want (0, true)", raw, ceiling, got, ok)
			}
			return
		}
		n, err := strconv.ParseInt(trimmed, 10, 32)
		wantOK := err == nil && n >= 0 && n <= int64(ceiling)
		if ok != wantOK || ok && int64(got) != n || !ok && got != 0 {
			t.Fatalf("web.ParseBounded(%q, %d) = (%d, %v), parsed=(%d, %v)",
				raw, ceiling, got, ok, n, err)
		}
	})
}

func TestAParcelSumCoversItsLongestSideBeforeWriting(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, tt := range []struct {
		name    string
		longest int32
		sum     int32
		refused bool
	}{
		{name: "short sum", longest: 500, sum: 499, refused: true},
		{name: "equal", longest: 500, sum: 500},
		{name: "unmeasured sum", longest: 500},
		{name: "unmeasured longest", sum: 499},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			errs := (&VariantForm{
				SKU: "PARCEL", PriceCents: 100, ParcelLongestMM: tt.longest, ParcelSumMM: tt.sum,
			}).Validate(ctx)
			if got := errs["parcel_sum"] != ""; got != tt.refused {
				t.Errorf("parcel_sum refusal = %v, want %v; errors=%v", got, tt.refused, errs)
			}
		})
	}
}

func TestParcelAndSafetyBoundsGuardStoreCallers(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, tt := range []struct {
		name  string
		field string
		errs  func() map[string]string
	}{
		{name: "variant safety", field: "safety", errs: func() map[string]string {
			return (&VariantForm{SKU: "BOUND", PriceCents: 100,
				SafetyStock: safetyStockCeiling + 1}).Validate(ctx)
		}},
		{name: "variant longest", field: "parcel_longest", errs: func() map[string]string {
			return (&VariantForm{SKU: "BOUND", PriceCents: 100,
				ParcelLongestMM: carrier.MaxParcelLongestMM + 1}).Validate(ctx)
		}},
		{name: "variant sum", field: "parcel_sum", errs: func() map[string]string {
			return (&VariantForm{SKU: "BOUND", PriceCents: 100,
				ParcelSumMM: carrier.MaxParcelSumMM + 1}).Validate(ctx)
		}},
		{name: "variant weight", field: "parcel_weight", errs: func() map[string]string {
			return (&VariantForm{SKU: "BOUND", PriceCents: 100,
				ParcelWeightG: carrier.MaxParcelWeightG + 1}).Validate(ctx)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if errs := tt.errs(); errs[tt.field] == "" {
				t.Errorf("missing %q refusal: %v", tt.field, errs)
			}
		})
	}

	variantAtCeilings := (&VariantForm{
		SKU: "BOUND", PriceCents: 100, SafetyStock: safetyStockCeiling,
		ParcelLongestMM: carrier.MaxParcelLongestMM, ParcelSumMM: carrier.MaxParcelSumMM,
		ParcelWeightG: carrier.MaxParcelWeightG,
	}).Validate(ctx)
	for _, field := range []string{
		"safety", "parcel_longest", "parcel_sum", "parcel_weight",
	} {
		if variantAtCeilings[field] != "" {
			t.Errorf("variant ceiling %q refused: %v", field, variantAtCeilings)
		}
	}
}

// TestAMistypedPriceIsRefusedByEveryFormThatWritesOne. Two back-office forms
// write products.price_cents and compare_at_price_cents: the reprice box on
// /admin/stock and the variant form on /admin/products/{slug}. Both must tell a
// blank field from an unreadable one, because zero on a compare-at price is not
// an error — it is the stored value for "not on sale", so a collapsed figure
// publishes the product at full price with the discount silently dropped.
func TestAMistypedPriceIsRefusedByEveryFormThatWritesOne(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, price, compare, wantField string
	}{
		{name: "unreadable compare-at", price: "1000", compare: "1o000", wantField: "compare"},
		{name: "unreadable price", price: "12o", compare: "", wantField: "price"},
		{name: "negative compare-at", price: "1000", compare: "-1", wantField: "compare"},
		{name: "compare-at above the money ceiling", price: "1000",
			compare: strconv.FormatInt(money.MaxCents/100+1, 10), wantField: "compare"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			form := url.Values{"sku": {"SKU-1"}, "price": {tt.price}, "compare": {tt.compare}}
			r := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), i18n.En),
				http.MethodPost, "/admin/products/x/variants",
				strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			f, _, errs := variantFormOf(r)
			maps.Copy(errs, f.Validate(r.Context()))
			if errs[tt.wantField] == "" {
				t.Errorf("%s=%q was accepted; errs = %v", tt.wantField,
					cmp.Or(tt.compare, tt.price), errs)
			}
		})
	}
}

func TestAVariantSKUNeedsTheShapeTheColumnRequires(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, tt := range []struct {
		name, sku string
		want      string
	}{
		{name: "plain", sku: "TEE"},
		{name: "segmented", sku: "TEE-RED-M"},
		{name: "lowercase is raised", sku: "tee-red"},
		{name: "underscore", sku: "TEE_RED", want: i18n.T(ctx, i18n.KeyFormSKUFormat)},
		{name: "inner space", sku: "TEE RED M", want: i18n.T(ctx, i18n.KeyFormSKUFormat)},
		{name: "double hyphen", sku: "TEE--RED", want: i18n.T(ctx, i18n.KeyFormSKUFormat)},
		{name: "leading hyphen", sku: "-TEE", want: i18n.T(ctx, i18n.KeyFormSKUFormat)},
		{name: "trailing hyphen", sku: "TEE-", want: i18n.T(ctx, i18n.KeyFormSKUFormat)},
		{name: "non ASCII", sku: "紅色", want: i18n.T(ctx, i18n.KeyFormSKUFormat)},
		{name: "blank", sku: " ", want: i18n.T(ctx, i18n.KeyFormSKURequired)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			errs := (&VariantForm{SKU: tt.sku, PriceCents: 100}).Validate(ctx)
			if got := errs["sku"]; got != tt.want {
				t.Errorf("Validate(SKU %q) sku error = %q, want %q", tt.sku, got, tt.want)
			}
		})
	}
}

func TestAVariantWriteRefusedForItsSKUShapeNamesTheSKUField(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	err := fmt.Errorf("%w: %w", ErrRefused, &pgconn.PgError{
		Code: "23514", ConstraintName: "product_variants_sku_format",
	})
	errs, got := variantWriteError(ctx, "tee", err)
	if got != nil || errs["sku"] != i18n.T(ctx, i18n.KeyFormSKUFormat) {
		t.Errorf("variantWriteError(sku_format) = %v, %v; want the sku field error and no error", errs, got)
	}
}
