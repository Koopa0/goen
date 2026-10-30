package i18n

import (
	"strings"
	"testing"
)

// TestKeyInvoiceMember checks that the default invoice choice explains who owns
// the email and promises a guest no account.
func TestKeyInvoiceMember(t *testing.T) {
	for _, locale := range Locales() {
		text := T(WithLocale(t.Context(), locale), KeyInvoiceMember)
		words := []string{"結帳時填的電子郵件", "存入", "通知"}
		if locale == En {
			words = []string{"checkout email", "stored", "notified"}
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

// TestMessagesCheckoutInvoiceNoProvider checks that the checkout's invoice
// wording names no provider: a shopper knows the member carrier by its name on
// the form, not by who runs it.
func TestMessagesCheckoutInvoiceNoProvider(t *testing.T) {
	t.Parallel()
	for _, k := range []Key{
		KeyInvoiceMember, KeyInvoiceMobile, KeyFieldMobileBarcode,
		KeyInvoiceCompanyStored, KeyMobileBarcodeMissing,
	} {
		for _, locale := range Locales() {
			text := T(WithLocale(t.Context(), locale), k)
			for _, provider := range []string{"綠界", "ECPay"} {
				if strings.Contains(text, provider) {
					t.Errorf("%s %s names the provider %q: %s", locale, k, provider, text)
				}
			}
		}
	}
}

// TestMessagesInvoiceCarrierEnglish checks that English calls a 載具 an
// "invoice carrier" wherever Chinese says 載具, so it is not mistaken for a
// delivery carrier.
func TestMessagesInvoiceCarrierEnglish(t *testing.T) {
	t.Parallel()
	for k, message := range messages {
		if !strings.Contains(message.ZhHant, "載具") {
			continue
		}
		en := strings.ToLower(message.En)
		if !strings.Contains(en, "invoice carrier") {
			t.Errorf("%s says 載具 but its English does not say \"invoice carrier\": %s", k, message.En)
		}
		if strings.Count(en, "carrier") != strings.Count(en, "invoice carrier") {
			t.Errorf("%s says 載具 and its English has a bare \"carrier\", which reads as the parcel company: %s", k, message.En)
		}
	}
}
