package i18n

import (
	"fmt"
	"testing"
)

func TestWarrantySerialLengthRefusalNamesTheAcceptedLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale Locale
		want   string
	}{
		{En, "Keep the serial number within 60 characters."},
		{ZhHant, "序號請控制在 60 個字元以內。"},
	} {
		t.Run(tc.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			got := fmt.Sprintf(T(WithLocale(t.Context(), tc.locale), KeyWarrantySerialTooLong), 60)
			if got != tc.want {
				t.Errorf("serial length refusal = %q, want %q", got, tc.want)
			}
		})
	}
}
