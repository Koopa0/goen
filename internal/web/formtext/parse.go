// Package formtext is the text a person typed into a form, read on the server;
// it holds the bounded parse, the storable-text check, width folding, control
// characters, typed counts, the search term and per-field refusals; it does not
// hold what the browser is told a field accepts, which is package ui/fieldrule,
// or the drawn field, which is package ui/widget.
package formtext

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// MaxFormBytes bounds ordinary forms before field validation reads their text.
const MaxFormBytes = 64 << 10

// ErrUnstorable is a form with a name or value PostgreSQL cannot store as text:
// bytes that are not UTF-8, or a NUL, which is valid UTF-8 yet refused the
// same way. No browser sends either, the field validators count runes and
// would pass them, and the INSERT that reached PostgreSQL would answer 500.
var ErrUnstorable = errors.New("formtext: form carries text that cannot be stored")

// ParseForm reads a bounded form body, so no handler calls r.ParseForm directly
// and forgets the limit, and refuses a form CheckStorable refuses.
func ParseForm(w http.ResponseWriter, r *http.Request) error {
	return parseFormWithLimit(w, r, MaxFormBytes)
}

// ParseLongTextForm budgets for legal UTF-8 text whose percent encoding can
// triple each byte. The ordinary budget covers other fields and leaves room
// to render over-limit text as a field refusal.
func ParseLongTextForm(w http.ResponseWriter, r *http.Request, runes int) error {
	return parseFormWithLimit(w, r, MaxFormBytes+int64(runes)*utf8.UTFMax*3)
}

func parseFormWithLimit(w http.ResponseWriter, r *http.Request, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("parse form: %w", err)
	}
	return CheckStorable(r.Form)
}

// CheckStorable answers ErrUnstorable for a parsed form, the query included, with
// a name or value that is not storable text.
func CheckStorable(form url.Values) error {
	for name, values := range form {
		if !storableText(name) {
			return ErrUnstorable
		}
		for _, v := range values {
			if !storableText(v) {
				return ErrUnstorable
			}
		}
	}
	return nil
}

func storableText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
