package shipping

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

func TestVersionFormKeepsRawFieldsAndRefusesTheOwningAmount(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, fee, threshold, field  string
		feeDollars, thresholdDollars int64
	}{
		{name: "ceiling", fee: fmt.Sprint(MaxFee / 100), threshold: fmt.Sprint(money.MaxCents / 100), feeDollars: MaxFee / 100, thresholdDollars: money.MaxCents / 100},
		{name: "blank optional", fee: " 123 ", threshold: " ", feeDollars: 123},
		{name: "bad fee", fee: "1e3", threshold: "1000", field: "version_fee", thresholdDollars: 1000},
		{name: "past fee ceiling", fee: "5001", threshold: "1000", field: "version_fee", feeDollars: 5001, thresholdDollars: 1000},
		{name: "negative fee", fee: "-1", field: "version_fee", feeDollars: -1},
		{name: "bad threshold", fee: "100", threshold: "not money", field: "version_free_over", feeDollars: 100},
		{name: "past threshold ceiling", fee: "100", threshold: "100000001", field: "version_free_over", feeDollars: 100, thresholdDollars: 100000001},
		{name: "negative threshold", fee: "100", threshold: "-1", field: "version_free_over", feeDollars: 100, thresholdDollars: -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			values := url.Values{"method": {"owned"}, "name": {" Raw name "}, "name_en": {" Raw name EN "}, "carrier": {" Raw carrier "}, "carrier_en": {" Raw carrier EN "}, "fee": {tt.fee}, "free_over": {tt.threshold}}
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/shipping/version", strings.NewReader(values.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			v, draft, errs := versionFormOf(r)
			wantDraft := admin.VersionDraft{MethodID: "owned", Name: " Raw name ", NameEn: " Raw name EN ", Carrier: " Raw carrier ", CarrierEn: " Raw carrier EN ", Fee: tt.fee, FreeOver: tt.threshold}
			if diff := cmp.Diff(wantDraft, draft); diff != "" {
				t.Errorf("raw version draft (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(ShippingVersion{MethodID: "owned", Name: " Raw name ", NameEn: " Raw name EN ", Carrier: " Raw carrier ", CarrierEn: " Raw carrier EN ", FeeDollars: tt.feeDollars, FreeOverDollars: tt.thresholdDollars}, v); diff != "" {
				t.Errorf("parsed version (-want +got):\n%s", diff)
			}
			wantErrors := map[string]string{}
			if tt.field != "" {
				key, maximum := i18n.KeyFormMethodFee, int64(MaxFee)
				if tt.field == "version_free_over" {
					key, maximum = i18n.KeyFormMethodFreeOver, money.MaxCents
				}
				wantErrors[tt.field] = fmt.Sprintf(i18n.T(r.Context(), key), money.TWD(maximum))
			}
			if diff := cmp.Diff(wantErrors, errs); diff != "" {
				t.Errorf("refused fields (-want +got):\n%s", diff)
			}
		})
	}
}

func FuzzVersionFormRetainsTheDraftAndBoundsAcceptedAmounts(f *testing.F) {
	for _, seed := range []struct{ name, fee, threshold string }{
		{"Delivery", "5000", "100000000"}, {"", "0", ""}, {"Delivery", "5001", "100000001"}, {"Delivery", "1e3", "bad"}, {"Delivery", "9223372036854775808", "-1"},
	} {
		f.Add(seed.name, seed.fee, seed.threshold)
	}
	f.Fuzz(func(t *testing.T, name, fee, threshold string) {
		values := url.Values{"method": {"owned"}, "name": {name}, "fee": {fee}, "free_over": {threshold}}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/shipping/version", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		v, draft, errs := versionFormOf(r)
		if draft.Name != name || draft.Fee != fee || draft.FreeOver != threshold {
			t.Fatalf("form parsing discarded raw fields: %+v", draft)
		}
		if len(errs) == 0 && (strings.TrimSpace(v.Name) == "" || v.FeeDollars < 0 || v.FeeDollars > MaxFee/100 || v.FreeOverDollars < 0 || v.FreeOverDollars > money.MaxCents/100) {
			t.Fatalf("accepted version outside the established limits: %+v", v)
		}
	})
}

func TestShippingAmountRefusalsNameTheMoneyCeiling(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			request := func() *http.Request {
				values := url.Values{"name": {"Delivery"}, "fee": {"unreadable"}, "free_over": {"unreadable"}}
				r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/shipping", strings.NewReader(values.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				return r
			}
			_, _, methodErrors := methodFormOf(request())
			_, _, versionErrors := versionFormOf(request())
			boundErrors := (&NewMethod{Code: "bounded", Destination: "address", Name: "Delivery", FeeDollars: MaxFee/100 + 1, FreeOverDollars: money.MaxCents/100 + 1}).Validate(ctx)
			for _, tt := range []struct {
				name          string
				errs          map[string]string
				fee, freeOver string
			}{
				{name: "new method parsing", errs: methodErrors, fee: "fee", freeOver: "free_over"},
				{name: "version parsing", errs: versionErrors, fee: "version_fee", freeOver: "version_free_over"},
				{name: "new method bounds", errs: boundErrors, fee: "fee", freeOver: "free_over"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					if got := tt.errs[tt.fee]; !strings.Contains(got, "NT$5,000") || strings.Contains(got, "%!") {
						t.Errorf("fee refusal=%q, want the formatted NT$5,000 ceiling", got)
					}
					if got := tt.errs[tt.freeOver]; !strings.Contains(got, "NT$100,000,000") || strings.Contains(got, "%!") {
						t.Errorf("threshold refusal=%q, want the formatted NT$100,000,000 ceiling", got)
					}
				})
			}
		})
	}
}
