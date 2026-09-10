package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func formRequest(t *testing.T, values url.Values) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/", strings.NewReader(values.Encode()),
	)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestShippingMoneyParsingDoesNotTurnTyposIntoFreeShipping(t *testing.T) {
	base := url.Values{
		"code": {"parcel"}, "destination": {"address"}, "name": {"Parcel"},
		"fee": {"0"}, "free_over": {""},
	}
	for _, tc := range []struct {
		field string
		raw   string
	}{
		{"fee", ""}, {"fee", "12o"}, {"fee", "-3"},
		{"fee", "999999999999999999999999"},
		{"free_over", "12o"}, {"free_over", "-3"},
		{"free_over", "999999999999999999999999"},
	} {
		t.Run(tc.field+"_"+tc.raw, func(t *testing.T) {
			values := base.Clone()
			values.Set(tc.field, tc.raw)
			_, _, errs := methodFormOf(formRequest(t, values))
			if errs[tc.field] == "" {
				t.Fatalf("%s=%q became a legal zero: %v", tc.field, tc.raw, errs)
			}
		})
	}
	_, _, errs := methodFormOf(formRequest(t, base))
	if errs["fee"] != "" || errs["free_over"] != "" {
		t.Fatalf("explicit zero fee / blank optional threshold refused: %v", errs)
	}
}

func TestCouponOptionalNumbersPreserveParseFailures(t *testing.T) {
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
				errs := couponFormOf(formRequest(t, values)).Validate(t.Context())
				if errs[field] == "" {
					t.Fatalf("%s=%q became a valid zero policy: %v", field, raw, errs)
				}
			})
		}
	}
	if errs := couponFormOf(formRequest(t, base)).Validate(t.Context()); len(errs) != 0 {
		t.Fatalf("blank optional coupon fields refused: %v", errs)
	}
}

func TestABlankPrefixSetIsDistinctFromAMissingCreationPrefix(t *testing.T) {
	set, message := parsePrefixes(t.Context(), "  , ; \n\t")
	if message != "" {
		t.Fatalf("blank replacement was refused: %q", message)
	}
	if set == nil {
		t.Fatal("blank replacement parsed as nil, want a non-nil empty set")
	}
	if len(set) != 0 {
		t.Fatalf("blank replacement parsed as %v, want an empty set", set)
	}

	if _, message := parseRequiredPrefixes(t.Context(), "  , ; \n\t"); message == "" {
		t.Fatal("blank prefixes were accepted while creating a zone")
	}
}

func FuzzParsePrefixes(f *testing.F) {
	for _, seed := range []string{
		"", "000", "001, 002;003\n004", "001 001", "12", "1000", "abc", "１２３",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		prefixes, message := parsePrefixes(t.Context(), input)
		required, requiredMessage := parseRequiredPrefixes(t.Context(), input)
		if message != "" {
			if requiredMessage == "" {
				t.Fatalf("parseRequiredPrefixes(%q) accepted input refused by parsePrefixes", input)
			}
			return
		}

		seen := make(map[string]bool, len(prefixes))
		for _, prefix := range prefixes {
			if len(prefix) != 3 || prefix[0] < '0' || prefix[0] > '9' ||
				prefix[1] < '0' || prefix[1] > '9' || prefix[2] < '0' || prefix[2] > '9' {
				t.Fatalf("parsePrefixes(%q) returned non-prefix %q", input, prefix)
			}
			if seen[prefix] {
				t.Fatalf("parsePrefixes(%q) returned duplicate %q", input, prefix)
			}
			seen[prefix] = true
		}

		if len(prefixes) == 0 {
			if prefixes == nil {
				t.Fatalf("parsePrefixes(%q) returned nil for an accepted empty replacement", input)
			}
			if requiredMessage == "" {
				t.Fatalf("parseRequiredPrefixes(%q) accepted an empty creation set", input)
			}
			return
		}
		if requiredMessage != "" || !slices.Equal(required, prefixes) {
			t.Fatalf("parseRequiredPrefixes(%q) = %v/%q, want %v/empty", input,
				required, requiredMessage, prefixes)
		}
	})
}
