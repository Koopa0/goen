package i18n

import (
	"fmt"
	"strings"
	"testing"
)

func TestShippingSurchargeRefusalUsesTheDeskTerm(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale      Locale
		term, clear string
	}{
		{locale: ZhHant, term: "分區加價", clear: "留空表示不加價"},
		{locale: En, term: "zone surcharge", clear: "leave it blank for no surcharge"},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := WithLocale(t.Context(), tt.locale)
			got := fmt.Sprintf(T(ctx, KeyFormShippingSurcharge), "NT$5,000")
			for _, want := range []string{tt.term, "NT$5,000", tt.clear} {
				if !strings.Contains(got, want) {
					t.Errorf("surcharge refusal=%q, want %q", got, want)
				}
			}
		})
	}
}

func TestShippingRefusalFitsFormsAndRowButtons(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale      Locale
		rule        string
		formActions []string
	}{
		{locale: ZhHant, rule: "配送規則", formActions: []string{"填寫", "再送出"}},
		{locale: En, rule: "delivery rule", formActions: []string{"entries", "submit again"}},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := WithLocale(t.Context(), tt.locale)
			got := T(ctx, KeyAdminShipRefused)
			if !strings.Contains(got, tt.rule) {
				t.Errorf("shipping refusal=%q, want the rule explanation %q", got, tt.rule)
			}
			for _, action := range tt.formActions {
				if strings.Contains(got, action) {
					t.Errorf("shipping refusal=%q asks a row-button user to %q", got, action)
				}
			}
		})
	}
}
