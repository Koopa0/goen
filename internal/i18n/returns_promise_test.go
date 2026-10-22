package i18n

import (
	"strings"
	"testing"
)

// TestMessagesNoExchange checks that no message offers an exchange.
//
// goen has returns but no exchange flow, so no sentence may offer one.
func TestMessagesNoExchange(t *testing.T) {
	for k, m := range messages {
		if strings.Contains(m.ZhHant, "換貨") || strings.Contains(strings.ToLower(m.En), "exchange") {
			t.Errorf("%s offers an exchange goen does not have: %q / %q", k, m.ZhHant, m.En)
		}
	}
}
