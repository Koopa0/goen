package web

import (
	"strconv"
	"strings"
)

// maxCount bounds a count typed into a back-office form.
const maxCount = 1_000_000

// ParseCount reads an optional count from a form field: blank is zero, and
// anything that is not a whole number from 0 to a million is not ok.
func ParseCount(s string) (n int32, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	v, err := strconv.ParseInt(s, 10, 32)
	return int32(v), err == nil && v >= 0 && v <= maxCount
}
