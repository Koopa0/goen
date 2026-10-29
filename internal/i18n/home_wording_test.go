package i18n

import (
	"strings"
	"testing"
)

// TestMessagesHomeWording checks that the home desk names its carousel and
// promises no schedule.
//
// A home slide or promotion strip goes live the moment it is saved: its insert
// sets only the end, and the start is the database default. 排程 and "schedule"
// promised what the form cannot do (#1261). The property is the word, so
// rewording a sentence does not break this and one stray 排程 in any home desk
// message does.
func TestMessagesHomeWording(t *testing.T) {
	t.Parallel()

	for k, want := range map[Key]struct{ zh, en string }{
		KeyAdminHomeScheduled: {zh: "首頁輪播", en: "carousel"},
		KeyAdminHomeAdd:       {zh: "加入輪播", en: "carousel"},
	} {
		m := messages[k]
		if !strings.Contains(m.ZhHant, want.zh) {
			t.Errorf("%s zh-Hant = %q, want it to say %s", k, m.ZhHant, want.zh)
		}
		if !strings.Contains(strings.ToLower(m.En), want.en) {
			t.Errorf("%s en = %q, want it to say %s", k, m.En, want.en)
		}
	}

	for k, m := range messages {
		if !strings.HasPrefix(string(k), "admin.home.") {
			continue
		}
		if strings.Contains(m.ZhHant, "排程") {
			t.Errorf("%s zh-Hant = %q, want no 排程: a slide starts when it is saved", k, m.ZhHant)
		}
		if strings.Contains(strings.ToLower(m.En), "schedul") {
			t.Errorf("%s en = %q, want no schedule: a slide starts when it is saved", k, m.En)
		}
	}
}
