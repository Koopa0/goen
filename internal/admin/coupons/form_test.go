package coupons

import (
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/discount/coupon"
	"github.com/koopa0/goen/internal/i18n"
)

func formRequest(t *testing.T, values url.Values) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/", strings.NewReader(values.Encode()),
	)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

// TestForm_ValidateDollarOverflow: coupon dollar inputs are bounded before they are multiplied.
func TestForm_ValidateDollarOverflow(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	errs := (&Form{
		Code: "OVERFLOW", Description: "overflow", Kind: "percent", Value: 10,
		CapDollars: math.MaxInt64, MinSpendDollars: math.MaxInt64, PerCustomer: 1,
	}).Validate(ctx)
	if errs["cap"] == "" || errs["min"] == "" {
		t.Fatalf("coupon overflow fields were accepted: %v", errs)
	}
}

// The desk stores the code checkout looks up: a code the desk accepts is the
// one checkout's Normalise finds, and a code checkout could never find is
// refused here rather than stored.
func TestForm_ValidateCodeMatchesCheckout(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, typed := range []string{"summer-10", "  Spring \t", "\u3000ab\u00a0", "a", "-AB", "AB C", "ＡＢＣ", strings.Repeat("a", 33)} {
		f := &Form{Code: typed, Description: "test", Kind: coupon.FreeShipping, PerCustomer: 1}
		errs := f.Validate(ctx)
		if f.Code != coupon.Normalise(typed) {
			t.Errorf("the desk stores %q as %q; checkout looks it up as %q", typed, f.Code, coupon.Normalise(typed))
		}
		if refused := errs["code"] != ""; refused == coupon.ValidCode(f.Code) {
			t.Errorf("the desk refused %q: %v; checkout says %q is a valid code: %v", typed, refused, f.Code, !refused)
		}
	}
}

// TestFormOfParseFailures: optional numbers preserve their parse failures.
func TestFormOfParseFailures(t *testing.T) {
	base := url.Values{
		"code": {"OK"}, "description": {"test"}, "kind": {"percent"},
		"value": {"10"}, "cap": {""}, "min": {""}, "max": {""},
		"percustomer": {"1"}, "days": {""},
	}
	for _, field := range []string{"cap", "min", "max", "percustomer", "days"} {
		for _, raw := range []string{"12o", "-3", "999999999999999999999999"} {
			t.Run(field+"_"+raw, func(t *testing.T) {
				values := base.Clone()
				values.Set(field, raw)
				errs := formOf(formRequest(t, values)).Validate(t.Context())
				if errs[field] == "" {
					t.Fatalf("%s=%q became a valid zero policy: %v", field, raw, errs)
				}
			})
		}
	}
	if errs := formOf(formRequest(t, base)).Validate(t.Context()); len(errs) != 0 {
		t.Fatalf("blank optional coupon fields refused: %v", errs)
	}
}
