package i18n

import (
	"strings"
	"testing"
	"unicode"
)

// A mark between two ASCII characters is part of a token — 1,000, 09:00, 8:3,
// a URL, a format verb — and stays as written.
func TestChinesePunctuationIsFullWidth(t *testing.T) {
	if len(messages) == 0 {
		t.Fatal("the catalogue is empty; this test would pass on nothing")
	}

	for k, m := range messages {
		if mark, found := halfWidthMarkBesideHan(m.ZhHant); found {
			t.Errorf("%s: ASCII %q sits against Chinese in %q; write it full-width (，：；？！（）)", k, mark, m.ZhHant)
		}
	}
}

func TestChineseNeverUsesTheHonorificYou(t *testing.T) {
	for k, m := range messages {
		if strings.Contains(m.ZhHant, "您") {
			t.Errorf("%s says 您 where goen says 你: %q", k, m.ZhHant)
		}
	}
}

// halfWidthMarkBesideHan reports the first ASCII , : ; ? ! ( ) whose nearest
// non-space neighbour on either side is Chinese.
func halfWidthMarkBesideHan(s string) (rune, bool) {
	rs := []rune(s)
	for i, r := range rs {
		if !strings.ContainsRune(",:;?!()", r) {
			continue
		}
		if chineseAt(rs, i, -1) || chineseAt(rs, i, 1) {
			return r, true
		}
	}
	return 0, false
}

func chineseAt(rs []rune, from, step int) bool {
	for i := from + step; i >= 0 && i < len(rs); i += step {
		if rs[i] == ' ' {
			continue
		}
		r := rs[i]
		return unicode.Is(unicode.Han, r) ||
			(r >= 0x3000 && r <= 0x303F) || // CJK symbols and punctuation: 、。「」
			(r >= 0xFF00 && r <= 0xFFEF) // full-width forms: ，：（）
	}
	return false
}
