package web

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// maxCount bounds a count typed into a back-office form.
const maxCount = 1_000_000

// ParseCount reads an optional count from a form field: blank is zero, and
// anything that is not a whole number from 0 to a million is not ok.
func ParseCount(s string) (n int32, ok bool) { return ParseBounded(s, maxCount) }

// ParseBounded reads a non-negative whole number whose blank and zero forms both
// mean zero. The boolean keeps unreadable and out-of-range input distinct from
// that valid zero until the handler can render a field refusal.
func ParseBounded(s string, ceiling int32) (n int32, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil || v < 0 || v > int64(ceiling) {
		return 0, false
	}
	return int32(v), true
}

// MinSearchRunes is the shortest back-office search that is a search. Counted in
// RUNES because two Chinese characters are a meaningful surname and two bytes
// are half of one.
const MinSearchRunes = 2

// maxSearchRunes bounds a search term; a longer one is cut, since no name or
// SKU is that long and the term is repeated in every link of the list.
const maxSearchRunes = 100

func SearchTerm(raw string) string {
	term := strings.TrimSpace(raw)
	if utf8.RuneCountInString(term) > maxSearchRunes {
		term = string([]rune(term)[:maxSearchRunes])
	}
	return term
}

var slugShape = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidSlug is the shape of an address segment goen mints: lower-case words
// joined by single hyphens.
func ValidSlug(s string) bool { return slugShape.MatchString(s) }

// ParseCountOrInvalid is ParseCount for a form field whose blank means "no
// limit": a malformed value becomes -1, which the form's Validate refuses by
// name, where collapsing it to zero would turn a typo into an unbounded promotion.
func ParseCountOrInvalid(s string) int32 {
	n, ok := ParseCount(s)
	if !ok {
		return -1
	}
	return n
}
