// Package web holds the HTTP helpers every feature shares: rendering a templ
// component, htmx detection, form-text limits and same-site paths. It knows
// nothing about any single feature.
package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/a-h/templ"
)

// MaxFormBytes bounds ordinary forms before field validation reads their text.
const MaxFormBytes = 64 << 10

// ErrFormText is a form with a name or value PostgreSQL cannot store as text:
// bytes that are not UTF-8, or a NUL, which is valid UTF-8 yet refused the
// same way. No browser sends either, the field validators count runes and
// would pass them, and the INSERT that reached PostgreSQL would answer 500.
var ErrFormText = errors.New("web: form carries text that cannot be stored")

// ParseForm reads a bounded form body, so no handler calls r.ParseForm directly
// and forgets the limit, and refuses a form CheckFormText refuses.
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
	return CheckFormText(r.Form)
}

// CheckFormText answers ErrFormText for a parsed form, the query included, with
// a name or value that is not storable text.
func CheckFormText(form url.Values) error {
	for name, values := range form {
		if !storableText(name) {
			return ErrFormText
		}
		for _, v := range values {
			if !storableText(v) {
				return ErrFormText
			}
		}
	}
	return nil
}

func storableText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// IsHTMX reports whether htmx issued this request. Its absence means a plain
// browser navigation, so the handler must answer with a whole page.
func IsHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// Render writes c to the response with the given status, rendering into memory
// first: a template that fails halfway would otherwise leave a truncated body
// under an already-sent 200.
func Render(w http.ResponseWriter, r *http.Request, log *slog.Logger, status int, c templ.Component) {
	var buf bytes.Buffer
	if err := c.Render(r.Context(), &buf); err != nil {
		if errors.Is(r.Context().Err(), context.Canceled) {
			// The caller left, so the render stopped and nobody reads a 500. A
			// deadline is not this: it stays an error below.
			return
		}
		log.ErrorContext(r.Context(), "render component", "error", err, "path", r.URL.Path)
		// i18n-exempt: the render failed, so there is no page to translate onto
		// and no guaranteed locale. Both languages, so neither reader is left out.
		http.Error(w, "500 內部錯誤 / Internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		log.ErrorContext(r.Context(), "write response", "error", err, "path", r.URL.Path)
	}
}
