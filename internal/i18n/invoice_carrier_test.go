package i18n

import (
	"strings"
	"testing"
)

func TestDefaultInvoiceCarrierExplainsEmailOwnership(t *testing.T) {
	for _, locale := range Locales() {
		text := T(WithLocale(t.Context(), locale), KeyInvoiceMember)
		words := []string{"綠界", "結帳 Email", "留存", "通知"}
		if locale == En {
			words = []string{"ECPay", "checkout email", "stored", "notified"}
		}
		for _, word := range words {
			if !strings.Contains(text, word) {
				t.Errorf("%s default invoice choice omits %q", locale, word)
			}
		}
		for _, claim := range []string{"存入會員帳號", "your goen account"} {
			if strings.Contains(text, claim) {
				t.Errorf("%s promises an account to a guest", locale)
			}
		}
	}
}
