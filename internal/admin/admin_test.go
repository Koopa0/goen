package admin

import (
	"cmp"
	"errors"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestDollarInputsAreBoundedBeforeMultiplication(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)

	method := (&NewMethod{
		Code: "overflow", Destination: "address", Name: "Overflow",
		FeeDollars: math.MaxInt64, FreeOverDollars: math.MaxInt64,
	}).Validate(ctx)
	if method["fee"] == "" || method["free_over"] == "" {
		t.Fatalf("shipping overflow fields were accepted: %v", method)
	}

	coupon := (&CouponForm{
		Code: "OVERFLOW", Description: "overflow", Kind: "percent", Value: 10,
		CapDollars: math.MaxInt64, MinSpendDollars: math.MaxInt64, PerCustomer: 1,
	}).Validate(ctx)
	if coupon["cap"] == "" || coupon["min"] == "" {
		t.Fatalf("coupon overflow fields were accepted: %v", coupon)
	}

	if err := (&Store{}).CreateTier(ctx, "overflow", "Overflow", "",
		math.MaxInt64, math.MaxInt64); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tier dollar/percent overflow = %v, want ErrInvalid", err)
	}
	if _, ok := positiveDollarsToCents("9223372036854775807", MaxPriceCents); ok {
		t.Fatal("MaxInt64 dollars was multiplied into an apparently valid allowance")
	}
	if got, ok := positiveDollarsToCents("100000000", MaxPriceCents); !ok || got != MaxPriceCents {
		t.Fatalf("exact money ceiling = %d/%t, want %d/true", got, ok, MaxPriceCents)
	}
}

func TestWorkerAgeSecondsSaturateInsteadOfWrappingHealthy(t *testing.T) {
	t.Parallel()
	got := durationFromSeconds(math.MaxInt64)
	if got != time.Duration(math.MaxInt64) {
		t.Fatalf("durationFromSeconds(MaxInt64) = %v, want saturation at %v",
			got, time.Duration(math.MaxInt64))
	}
	view := pages.WorkerHealthView{
		OutboxPending:        1,
		OutboxOldest:         got,
		OutboxStaleAfter:     OutboxStaleAfter,
		CopurchaseEverBuilt:  true,
		CopurchaseAge:        got,
		CopurchaseStaleAfter: CopurchaseStaleAfter,
	}
	if view.OutboxHealthy() || view.RecommendHealthy() {
		t.Fatal("a timestamp beyond Go's duration range wrapped into a healthy worker")
	}
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	if strings.Contains(view.OutboxText(ctx), "-") || strings.Contains(view.RecommendText(ctx), "-") {
		t.Fatalf("saturated ages rendered negative: %q / %q",
			view.OutboxText(ctx), view.RecommendText(ctx))
	}
}

func TestParseStatusAcceptsOnlyTheFulfilmentLifecycle(t *testing.T) {
	t.Parallel()
	for _, status := range statuses {
		if got := ParseStatus(string(status.value)); got != status.value {
			t.Errorf("ParseStatus(%q) = %q, want the same status", status.value, got)
		}
		if got := StatusLabel(t.Context(), status.value); got == "" || got == string(status.value) {
			t.Errorf("StatusLabel(%q) = %q, want a catalogue label", status.value, got)
		}
	}
	for _, status := range []string{"", "all", "paid", "refunded", "PENDING", " pending "} {
		if got := ParseStatus(status); got != "" {
			t.Errorf("ParseStatus(%q) = %q, want all-status fallback", status, got)
		}
	}
}

func TestEveryTransitionStaysInsideTheFulfilmentLifecycle(t *testing.T) {
	t.Parallel()
	for _, current := range statuses {
		for _, next := range NextStatuses(current.value) {
			if !next.Known() {
				t.Errorf("NextStatuses(%q) contains unknown state %q", current.value, next)
			}
		}
	}
}

// TestTheAdminCatalogueIsTheFulfilmentClosedSet holds the two halves together:
// pages.FulfillmentStatuses is what cart, account and the queue carry, and
// statuses is what ParseStatus and the tabs offer, so a state added to one
// and forgotten in the other is a filter that cannot name it or a label that
// never appears.
func TestTheAdminCatalogueIsTheFulfilmentClosedSet(t *testing.T) {
	t.Parallel()
	if len(statuses) != len(pages.FulfillmentStatuses) {
		t.Fatalf("admin catalogue has %d states, pages.FulfillmentStatuses has %d",
			len(statuses), len(pages.FulfillmentStatuses))
	}
	for i, want := range pages.FulfillmentStatuses {
		if statuses[i].value != want {
			t.Errorf("statuses[%d] = %q, want %q — the queue order drifted from the closed set",
				i, statuses[i].value, want)
		}
	}
}

// TestStatusLabelRendersARetiredStatus holds the reason StatusLabel is not a
// panic: audit_events is append-only, so a row naming a state the shop no
// longer occupies must still open.
func TestStatusLabelRendersARetiredStatus(t *testing.T) {
	t.Parallel()
	const retired pages.FulfillmentStatus = "packing"
	if got := StatusLabel(t.Context(), retired); got != string(retired) {
		t.Errorf("StatusLabel(%q) = %q, want the raw value so the queue still loads",
			retired, got)
	}
}

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
		{name: "longest ceiling", raw: "5000", max: parcelLongestCeilingMM, want: 5000, ok: true},
		{name: "longest over ceiling", raw: "6000", max: parcelLongestCeilingMM},
		{name: "sum ceiling", raw: "15000", max: parcelSumCeilingMM, want: 15000, ok: true},
		{name: "weight ceiling", raw: "200000", max: parcelWeightCeilingG, want: 200000, ok: true},
		{name: "comma", raw: "10,000", max: parcelWeightCeilingG},
		{name: "safety ceiling", raw: "1000000", max: safetyStockCeiling, want: 1000000, ok: true},
		{name: "safety over ceiling", raw: "1000001", max: safetyStockCeiling},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseBoundedInt(tt.raw, tt.max)
			if got != tt.want || ok != tt.ok {
				t.Errorf("parseBoundedInt(%q, %d) = (%d, %v), want (%d, %v)",
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
			MaxWarrantyMonths, parcelLongestCeilingMM, parcelSumCeilingMM,
			parcelWeightCeilingG, safetyStockCeiling,
		}
		ceiling := maxima[int(choice)%len(maxima)]
		got, ok := parseBoundedInt(raw, ceiling)
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			if got != 0 || !ok {
				t.Fatalf("parseBoundedInt(%q, %d) = (%d, %v), want (0, true)", raw, ceiling, got, ok)
			}
			return
		}
		n, err := strconv.ParseInt(trimmed, 10, 32)
		wantOK := err == nil && n >= 0 && n <= int64(ceiling)
		if ok != wantOK || ok && int64(got) != n || !ok && got != 0 {
			t.Fatalf("parseBoundedInt(%q, %d) = (%d, %v), parsed=(%d, %v)",
				raw, ceiling, got, ok, n, err)
		}
	})
}

func TestOptionalRunDaysDoNotTurnMalformedInputIntoNoExpiry(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, raw := range []string{"forever", "1.5", "-1", "1000001"} {
		days := small(raw)
		if days >= 0 {
			t.Fatalf("small(%q) = %d, want an invalid sentinel", raw, days)
		}
		if errs := (&BannerForm{Message: "Sale", Days: days}).Validate(ctx); errs["days"] == "" {
			t.Errorf("BannerForm accepted malformed days %q as an unbounded banner", raw)
		}
		if errs := (&HeroForm{
			Headline: "Sale", PrimaryLabel: "Shop", PrimaryHref: "/deals", Days: days,
		}).Validate(ctx); errs["days"] == "" {
			t.Errorf("HeroForm accepted malformed days %q as an unbounded slide", raw)
		}
	}
	if got := small(""); got != 0 {
		t.Errorf("small(blank) = %d, want the documented no-expiry value 0", got)
	}
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
				ParcelLongestMM: parcelLongestCeilingMM + 1}).Validate(ctx)
		}},
		{name: "variant sum", field: "parcel_sum", errs: func() map[string]string {
			return (&VariantForm{SKU: "BOUND", PriceCents: 100,
				ParcelSumMM: parcelSumCeilingMM + 1}).Validate(ctx)
		}},
		{name: "variant weight", field: "parcel_weight", errs: func() map[string]string {
			return (&VariantForm{SKU: "BOUND", PriceCents: 100,
				ParcelWeightG: parcelWeightCeilingG + 1}).Validate(ctx)
		}},
		{name: "method longest", field: "max_parcel_longest", errs: func() map[string]string {
			return (&NewMethod{Code: "bound", Destination: "address", Name: "Bound",
				MaxParcelLongestMM: parcelLongestCeilingMM + 1}).Validate(ctx)
		}},
		{name: "method sum", field: "max_parcel_sum", errs: func() map[string]string {
			return (&NewMethod{Code: "bound", Destination: "address", Name: "Bound",
				MaxParcelSumMM: parcelSumCeilingMM + 1}).Validate(ctx)
		}},
		{name: "method weight", field: "max_parcel_weight", errs: func() map[string]string {
			return (&NewMethod{Code: "bound", Destination: "address", Name: "Bound",
				MaxParcelWeightG: parcelWeightCeilingG + 1}).Validate(ctx)
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
		ParcelLongestMM: parcelLongestCeilingMM, ParcelSumMM: parcelSumCeilingMM,
		ParcelWeightG: parcelWeightCeilingG,
	}).Validate(ctx)
	methodAtCeilings := (&NewMethod{
		Code: "bound", Destination: "address", Name: "Bound",
		MaxParcelLongestMM: parcelLongestCeilingMM, MaxParcelSumMM: parcelSumCeilingMM,
		MaxParcelWeightG: parcelWeightCeilingG,
	}).Validate(ctx)
	for _, field := range []string{
		"safety", "parcel_longest", "parcel_sum", "parcel_weight",
	} {
		if variantAtCeilings[field] != "" {
			t.Errorf("variant ceiling %q refused: %v", field, variantAtCeilings)
		}
	}
	for _, field := range []string{
		"max_parcel_longest", "max_parcel_sum", "max_parcel_weight",
	} {
		if methodAtCeilings[field] != "" {
			t.Errorf("method ceiling %q refused: %v", field, methodAtCeilings)
		}
	}
}

// TestAReceiptIsAlwaysPositive proves a receipt cannot take stock away, which is
// what keeps a delivery distinguishable from a correction.
func TestAReceiptIsAlwaysPositive(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  int32
		wantY bool
	}{
		{name: "an ordinary delivery", in: "12", want: 12, wantY: true},
		{name: "one unit", in: "1", want: 1, wantY: true},
		{name: "surrounding space is not a typo worth refusing", in: "  8 ", want: 8, wantY: true},
		{name: "the ceiling", in: "10000", want: 10000, wantY: true},
		{name: "a correction typed into the receipt box", in: "-3"},
		{name: "nothing arrived is not a delivery", in: "0"},
		{name: "a warehouse invented by a typo", in: "10001"},
		{name: "empty", in: ""},
		{name: "not a number", in: "十二"},
		{name: "a fraction of a unit", in: "1.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseReceipt(tt.in)
			if ok != tt.wantY || got != tt.want {
				t.Errorf("ParseReceipt(%q) = %d, %v, want %d, %v",
					tt.in, got, ok, tt.want, tt.wantY)
			}
		})
	}
}

// TestAFundedOrderIsNotBadgedUnpaid holds the two halves of 'pending' apart: an
// order stays pending from the moment money arrives until a human picks it, and
// one paid entirely from store credit has no payment row at all. Committed alone
// means the shop has taken it on, owed == 0 alone means nothing is due, and
// either is enough to say it is not awaiting payment.
func TestAFundedOrderIsNotBadgedUnpaid(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	unpaid := i18n.T(ctx, i18n.KeyAdminStatusPending)
	ready := i18n.T(ctx, i18n.KeyAdminStatusReadyToPick)

	for _, tt := range []struct {
		name      string
		status    pages.FulfillmentStatus
		committed bool
		owed      int64
		want      string
	}{
		{name: "nobody has paid", status: "pending", owed: 65000, want: unpaid},
		{name: "the card cleared", status: "pending", committed: true, owed: 65000, want: ready},
		{name: "store credit covered it", status: "pending", owed: 0, want: ready},
		// Every other status answers from itself: only pending is two states
		// wearing one name.
		{name: "picking", status: "picking", committed: true, want: i18n.T(ctx, i18n.KeyAdminStatusPicking)},
		{name: "cancelled and unpaid", status: "cancelled", owed: 65000, want: i18n.T(ctx, i18n.KeyAdminStatusCancelled)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := FundedStatusLabel(ctx, tt.status, tt.committed, tt.owed); got != tt.want {
				t.Errorf("FundedStatusLabel(%q, committed=%v, owed=%d) = %q, want %q",
					tt.status, tt.committed, tt.owed, got, tt.want)
			}
		})
	}
}

// TestEveryRedirectNoticeHasAMessage asks the question the notice map cannot ask
// of itself: a handler answering 303 with "?done=1" and no entry here renders a
// blank page and tells the operator nothing. The map is hand-written; the corpus
// is the SOURCE, so a new redirect is covered the moment it is written.
func TestEveryRedirectNoticeHasAMessage(t *testing.T) {
	t.Parallel()

	// Every file in the package, not handler.go alone: the image and hero
	// handlers redirect too.
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list the package: %v", err)
	}
	// "?name=1", "&name=1", and a bare "name=1" returned by a helper, which is
	// how the image handlers write theirs.
	param := regexp.MustCompile(`[?&"]([a-z]+)=1`)
	found := map[string]bool{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, readErr := os.ReadFile(name) //nolint:gosec // G304: this package's own files
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		for _, m := range param.FindAllStringSubmatch(string(src), -1) {
			found[m[1]] = true
		}
	}
	if len(found) < 15 {
		t.Fatalf("only %d redirect parameters found; the parser stopped matching", len(found))
	}

	var missing []string
	for name := range found {
		if _, ok := adminNotices[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d redirect parameter(s) carry no message:\n  %s\n"+
			"The page renders nothing, so the operator cannot tell whether the "+
			"button did anything.", len(missing), strings.Join(missing, "\n  "))
	}

	// And the other direction: an entry naming a parameter no handler writes is
	// a message nothing can show, which is how a list grows past its subject.
	var orphaned []string
	for name := range adminNotices {
		if !found[name] {
			orphaned = append(orphaned, name)
		}
	}
	if len(orphaned) > 0 {
		sort.Strings(orphaned)
		t.Errorf("%d notice(s) name a parameter no redirect writes:\n  %s",
			len(orphaned), strings.Join(orphaned, "\n  "))
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
			compare: strconv.FormatInt(MaxPriceCents/100+1, 10), wantField: "compare"},
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
