package i18n

import (
	"slices"
	"strings"
	"testing"
)

// TestMessagesBackOfficeTerms checks the words #1041 retired and #1264 asked the back
// office to drop: the English never says "coupon" (the term is "discount
// code"), and the back office's Chinese says none of 額度 (store credit is
// 購物金), 限時 (a deal is 優惠), 優惠碼 (the code is 折扣碼) or 管理者 (the
// role is 管理員). The property is the word, so rewording a sentence does not
// break it and one stray old word does.
func TestMessagesBackOfficeTerms(t *testing.T) {
	t.Parallel()

	forms := make(map[Key][]string, len(messages))
	for k, m := range messages {
		forms[k] = []string{m.En}
		if one, ok := singulars[k]; ok {
			forms[k] = append(forms[k], one)
		}
	}
	for k, texts := range forms {
		for _, en := range texts {
			if strings.Contains(strings.ToLower(en), "coupon") {
				t.Errorf("%s en = %q, want Discount code, not coupon", k, en)
			}
		}
	}

	backOffice := []string{"admin.", "audit.", "twofactor.", "staff."}
	retired := []string{"額度", "限時", "優惠碼", "管理者"}
	for k, m := range messages {
		if !slices.ContainsFunc(backOffice, func(p string) bool { return strings.HasPrefix(string(k), p) }) {
			continue
		}
		for _, word := range retired {
			if strings.Contains(m.ZhHant, word) {
				t.Errorf("%s zh-Hant = %q, want no %s", k, m.ZhHant, word)
			}
		}
	}
}
