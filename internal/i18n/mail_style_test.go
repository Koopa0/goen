package i18n

import (
	"regexp"
	"strings"
	"testing"
)

// A letter keeps one colon style per language: full-width in Chinese, ASCII in
// English. A clock time and the colon of a URL are not punctuation.
var colonOutsideTokens = regexp.MustCompile(`(?:\D|^):(?:[^/\d]|$)`)

func TestMailKeepsOneColonStylePerLanguage(t *testing.T) {
	t.Parallel()

	seen := 0
	for k, m := range messages {
		if !strings.HasPrefix(string(k), "mail.") {
			continue
		}
		seen++
		if colonOutsideTokens.MatchString(m.ZhHant) {
			t.Errorf("%s: the Chinese text uses an ASCII colon: %q", k, m.ZhHant)
		}
		if strings.Contains(m.En, "：") {
			t.Errorf("%s: the English text uses a full-width colon: %q", k, m.En)
		}
	}
	if seen == 0 {
		t.Fatal("no mail message was checked")
	}
}

func TestEveryLetterLinkSitsOnItsOwnLine(t *testing.T) {
	t.Parallel()

	for k, m := range messages {
		if !strings.HasPrefix(string(k), "mail.order.") {
			continue
		}
		for _, text := range []string{m.ZhHant, m.En} {
			if strings.Contains(text, "%s") && strings.HasSuffix(text, "%s") && !strings.HasSuffix(text, "\n%s") {
				t.Errorf("%s: a link follows other words on its line: %q", k, text)
			}
		}
	}
}
