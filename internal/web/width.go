package web

import "strings"

// FoldWidth maps full-width ASCII (U+FF01–U+FF5E) and the ideographic space to
// their ASCII forms, so what a Zhuyin keyboard left in full-width mode types
// validates and matches like what was meant. It is not NFKC: half-width kana
// and the other compatibility characters stay as typed.
func FoldWidth(s string) string {
	if !strings.ContainsFunc(s, isFullWidth) {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '　':
			return ' '
		case r >= '！' && r <= '～':
			return r - 0xFEE0
		}
		return r
	}, s)
}

func isFullWidth(r rune) bool {
	return r == '　' || (r >= '！' && r <= '～')
}
