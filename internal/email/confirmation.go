package email

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Mask hides the local part and its length. It reflects submitted input, never
// whether an account or subscription exists.
func Mask(address string) string {
	address = Clean(address)
	if !Valid(address) {
		return ""
	}
	at := strings.LastIndexByte(address, '@')
	first, _ := utf8.DecodeRuneInString(address[:at])
	prefix := ""
	if unicode.IsLetter(first) || unicode.IsDigit(first) {
		prefix = string(first)
	}
	return prefix + "***" + address[at:]
}

// ReadMasked refuses full addresses and arbitrary query text. Only the display
// form produced by Mask may survive a confirmation-page redirect.
func ReadMasked(address string) string {
	address = Clean(address)
	if len(address) > Max+3 {
		return ""
	}
	at := strings.LastIndexByte(address, '@')
	if at < 0 {
		return ""
	}
	prefix, ok := strings.CutSuffix(address[:at], "***")
	if !ok {
		return ""
	}
	probe := "a"
	if prefix != "" {
		first, size := utf8.DecodeRuneInString(prefix)
		if size != len(prefix) || (!unicode.IsLetter(first) && !unicode.IsDigit(first)) {
			return ""
		}
		probe = prefix
	}
	if !Valid(probe + address[at:]) {
		return ""
	}
	return address
}
