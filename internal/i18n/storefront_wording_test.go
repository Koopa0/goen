package i18n

import "testing"

func TestStorefrontCopyDescribesPhotosChargesAndShipmentWithoutSystemTalk(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		key    Key
		zh, en string
	}{
		{KeyImagePlaceholder, "暫無商品照片", "No photo yet"},
		{KeyShippingSub, "以下為結帳時的配送費用。", "These are the delivery charges shown at checkout."},
		{KeyPayBody, "訂單已送出，商品已為你保留。完成付款後我們會盡快安排出貨。", "The order is placed and the stock is held for you. We will arrange shipment shortly after payment."},
	} {
		for locale, want := range map[Locale]string{ZhHant: tt.zh, En: tt.en} {
			if got := T(WithLocale(t.Context(), locale), tt.key); got != want {
				t.Errorf("%s in %s = %q, want %q", tt.key, locale.Tag(), got, want)
			}
		}
	}
}
