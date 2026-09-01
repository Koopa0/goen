package admin

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestParseStatusAcceptsOnlyTheFulfilmentLifecycle(t *testing.T) {
	t.Parallel()
	for _, status := range statuses {
		if got := ParseStatus(status.value); got != status.value {
			t.Errorf("ParseStatus(%q) = %q, want the same status", status.value, got)
		}
		if got := StatusLabel(t.Context(), status.value); got == "" || got == status.value {
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
			if ParseStatus(next) == "" {
				t.Errorf("NextStatuses(%q) contains unknown state %q", current.value, next)
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

// TestAFundedOrderIsNotBadgedUnpaid holds the two halves of 'pending' apart.
//
// An order stays pending from the moment money arrives until a human picks it,
// and one paid entirely from store credit has no payment row at all — so it sits
// there for good. Reading the status alone badged it 待付款 on the queue somebody
// works, beside the customer's own page saying 付款完成, and nothing would ever
// move it because no payment is coming.
//
// CLAUDE.md states the rule for exactly this caller: Committed alone means the
// shop has taken it on, owed == 0 alone means nothing is due, and either is
// enough to say it is not awaiting payment.
func TestAFundedOrderIsNotBadgedUnpaid(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	unpaid := i18n.T(ctx, i18n.KeyAdminStatusPending)
	ready := i18n.T(ctx, i18n.KeyAdminStatusReadyToPick)

	for _, tt := range []struct {
		name      string
		status    string
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
// blank page and tells the operator nothing.
//
// Three of the parameters this branch added were in exactly that state — a 折讓
// filed with the 財政部 confirmed nothing, a refused amount said nothing, and
// /admin/health answered two parameters its own handler never read. The map is
// hand-written; the corpus is the SOURCE, so a new redirect is covered the
// moment it is written.
func TestEveryRedirectNoticeHasAMessage(t *testing.T) {
	t.Parallel()

	// Every file in the package, not handler.go alone: the image and hero
	// handlers redirect too, and a corpus one file narrower reported five real
	// notices as orphans.
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list the package: %v", err)
	}
	// "?name=1", "&name=1", and a bare "name=1" returned by a helper — which is
	// how the image handlers write theirs, and matching only inside the
	// Redirect call reported five real notices as orphans.
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
