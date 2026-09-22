package i18n

import (
	"strings"
	"testing"
)

func TestContactDoesNotPromiseAReplyDeadline(t *testing.T) {
	for _, locale := range Locales() {
		ctx := WithLocale(t.Context(), locale)
		for _, key := range []Key{KeyContactHours, KeyContactSentBody} {
			for _, deadline := range []string{"10 分鐘", "一個工作天", "10 minutes", "one working day"} {
				if strings.Contains(T(ctx, key), deadline) {
					t.Errorf("%s (%s) promises an unsupported reply deadline %q", key, locale, deadline)
				}
			}
		}
		want := "留的信箱"
		if locale == En {
			want = "address you gave"
		}
		if !strings.Contains(T(ctx, KeyContactSentBody), want) {
			t.Errorf("%s confirmation does not tell the sender where the reply goes", locale)
		}
	}
}
