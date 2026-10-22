package i18n

import (
	"strings"
	"testing"
)

// TestMessagesNewsletterPromise checks that the newsletter messages promise
// occasional mail with a way to leave, and no cadence the shop does not keep.
func TestMessagesNewsletterPromise(t *testing.T) {
	for _, locale := range Locales() {
		ctx := WithLocale(t.Context(), locale)
		for _, key := range []Key{KeyNewsletterNote, KeyNewsletterConfirmBody, KeyNewsletterDoneBody, KeyMailNewsWelcomeBody} {
			body := T(ctx, key)
			cadence, leave := "不定期", "退訂"
			if locale == En {
				cadence, leave = "occasionally", "leave"
				if key == KeyNewsletterNote || key == KeyNewsletterDoneBody {
					leave = "unsubscribe"
				}
			}
			if !strings.Contains(body, cadence) || !strings.Contains(body, leave) {
				t.Errorf("%s (%s) must describe occasional mail and leaving the list", key, locale)
			}
		}
		for _, key := range []Key{KeyNewsletterNote, KeyNewsletterConfirmBody, KeyNewsletterDoneBody, KeyMailNewsWelcomeBody, KeyMailNewsConfirmBody} {
			body := strings.ToLower(T(ctx, key))
			for _, unsupported := range []string{"每月", "a month", "monthly"} {
				if strings.Contains(body, unsupported) {
					t.Errorf("%s (%s) promises unsupported cadence %q", key, locale, unsupported)
				}
			}
		}
	}
}
