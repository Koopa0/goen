package i18n

import (
	"strings"
	"testing"
)

func TestProductLabelRefusalDescribesTheSubmittedFacts(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale          Locale
		facts, internal string
	}{
		{locale: ZhHant, facts: "這組商品標示無法儲存", internal: "資料庫"},
		{locale: En, facts: "product label facts could not be saved", internal: "database"},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			got := T(WithLocale(t.Context(), tt.locale), KeyProductLabelRefused)
			if !strings.Contains(got, tt.facts) || strings.Contains(got, tt.internal) {
				t.Errorf("product label refusal = %q, want fact refusal %q without %q", got, tt.facts, tt.internal)
			}
		})
	}
}
