package i18n

import "testing"

func TestReturnRefusalsExplainTheRefusedControl(t *testing.T) {
	t.Parallel()
	tests := []struct {
		key    Key
		zh, en string
	}{
		{KeyReturnQuantityInvalid, "請填寫零或以上的整數。", "Enter a whole number of zero or more."},
		{KeyReturnReasonTooLong, "退貨原因最多 500 字。", "Keep the optional reason within 500 characters."},
		{KeyReturnReasonUnsupportedControls, "請移除退貨原因中不支援的控制字元。", "Remove unsupported control characters from the optional reason."},
	}
	for _, tt := range tests {
		for _, locale := range []Locale{ZhHant, En} {
			t.Run(string(tt.key)+"/"+string(locale), func(t *testing.T) {
				t.Parallel()
				want := tt.en
				if locale == ZhHant {
					want = tt.zh
				}
				if got := T(WithLocale(t.Context(), locale), tt.key); got != want {
					t.Errorf("T(%s,%s) = %q, want %q", tt.key, locale, got, want)
				}
			})
		}
	}
}
