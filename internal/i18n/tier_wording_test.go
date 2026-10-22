package i18n

import (
	"strings"
	"testing"
)

// TestMessagesTierWording checks that the back office names a membership level
// one way in each language.
//
// The back office names a membership level one way in each language: 等級 and
// "tier". #1192 was the English side drifting to "band" in three strings. The
// property is the term, not the sentence around it, so rewording a hint does
// not break this and a "band" in any tier message does.
func TestMessagesTierWording(t *testing.T) {
	t.Parallel()

	for _, k := range []Key{KeyAdminTierColTier, KeyAdminTierAdd, KeyAdminTierNameEnHint} {
		m := messages[k]
		if !strings.Contains(m.ZhHant, "等級") {
			t.Errorf("%s zh-Hant = %q, want it to say 等級", k, m.ZhHant)
		}
		if !strings.Contains(strings.ToLower(m.En), "tier") {
			t.Errorf("%s en = %q, want it to say tier", k, m.En)
		}
	}

	for k, m := range messages {
		if !strings.HasPrefix(string(k), "admin.tier.") {
			continue
		}
		if strings.Contains(strings.ToLower(m.En), "band") {
			t.Errorf("%s en = %q: a membership level is a tier, not a band", k, m.En)
		}
	}
}
