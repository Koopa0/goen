package admin

import (
	"cmp"
	"maps"
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

	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

func TestParseStatusAcceptsOnlyTheFulfilmentLifecycle(t *testing.T) {
	t.Parallel()
	for _, status := range pages.FulfillmentStatuses {
		if got := ParseStatus(string(status)); got != status {
			t.Errorf("ParseStatus(%q) = %q, want the same status", status, got)
		}
	}
	for _, status := range []string{"", "all", "paid", "refunded", "PENDING", " pending "} {
		if got := ParseStatus(status); got != "" {
			t.Errorf("ParseStatus(%q) = %q, want all-status fallback", status, got)
		}
	}
}

// TestEveryKnownReturnStatusHasAnAdminLabel holds the closed set together:
// a status with no catalogue entry must render as itself, never panic.
func TestEveryKnownReturnStatusHasAnAdminLabel(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, status := range []returns.Status{
			returns.StatusRequested,
			returns.StatusApproved,
			returns.StatusRejected,
			returns.StatusCompleted,
		} {
			label := ReturnStatusLabel(ctx, status)
			if label == "" || label == string(status) {
				t.Errorf("ReturnStatusLabel(%q) in %s = %q, want a catalogue label",
					status, locale, label)
			}
		}
	}
}

func TestUnknownReturnStatusLabelRendersAsItself(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	unknown := returns.Status("legacy_foo")
	if got := ReturnStatusLabel(ctx, unknown); got != "legacy_foo" {
		t.Fatalf("ReturnStatusLabel(%q) = %q, want the raw status", unknown, got)
	}
}

func TestEveryTransitionStaysInsideTheFulfilmentLifecycle(t *testing.T) {
	t.Parallel()
	for _, current := range pages.FulfillmentStatuses {
		for _, next := range NextStatuses(current) {
			if !next.Known() {
				t.Errorf("NextStatuses(%q) contains unknown state %q", current, next)
			}
		}
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
	// The upload refusals are written by media.UploadQuery, not as literals.
	for _, refusal := range []error{media.ErrTooLarge, media.ErrNotAnImage, media.ErrLosslessWebP, media.ErrBusy} {
		name, _, _ := strings.Cut(media.UploadQuery(refusal), "=")
		found[name] = true
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

// TestOnlyTheDispatchFormUsesTheCarrierAndTrackingNotice holds that a refusal
// names its own screen's problem: the carrier-and-tracking sentence once
// answered tiers, shipping, store credit, delivery correction and image reuse.
func TestOnlyTheDispatchFormUsesTheCarrierAndTrackingNotice(t *testing.T) {
	t.Parallel()
	for name, key := range adminNotices {
		if key == i18n.KeyAdminNoticeNeeds && name != "needs" {
			t.Errorf("?%s=1 answers with the dispatch form's notice", name)
		}
	}
}

func TestTheQueueFiltersAreTheirOwnClosedSet(t *testing.T) {
	t.Parallel()
	for _, tab := range queueTabs {
		if got := ParseQueueFilter(string(tab.filter)); got != tab.filter {
			t.Errorf("ParseQueueFilter(%q) = %q, want the same filter", tab.filter, got)
		}
	}
	for _, in := range []string{"", "all", "PENDING", " ready ", "paid"} {
		if got := ParseQueueFilter(in); got != admin.QueueAll {
			t.Errorf("ParseQueueFilter(%q) = %q, want every order", in, got)
		}
	}
	if got := ParseStatus("ready"); got != "" {
		t.Errorf("ParseStatus(ready) = %q: a transition must not be able to name a queue filter", got)
	}
}

func TestFundedMeansCommittedOrPaidWholeByCredit(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name         string
		committed    bool
		owed, credit int64
		want         bool
	}{
		{name: "card captured", committed: true, owed: 65000, want: true},
		{name: "credit paid it all", owed: 0, credit: 65000, want: true},
		{name: "credit paid part, card still owed", owed: 40000, credit: 25000, want: false},
		{name: "nothing paid", owed: 65000, want: false},
		{name: "free after a discount", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := funded(tt.committed, tt.owed, tt.credit); got != tt.want {
				t.Errorf("funded(%t, %d, %d) = %t, want %t", tt.committed, tt.owed, tt.credit, got, tt.want)
			}
		})
	}
}

func TestPaymentWithoutCardIsReadFromWhatIsOwed(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tt := range []struct {
		name         string
		owed, credit int64
		want         string
	}{
		{name: "credit paid it all", credit: 65000, want: "購物金全額折抵"},
		{name: "free after a discount", want: i18n.T(ctx, i18n.KeyAdminPayMethodFree)},
		{name: "still owed", owed: 65000, want: ""},
		{name: "credit reversed by a cancellation", owed: 65000, credit: 0, want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := paymentWithoutCard(ctx, tt.owed, tt.credit).Method; got != tt.want {
				t.Errorf("Method = %q, want %q", got, tt.want)
			}
		})
	}
}
