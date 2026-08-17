// Package i18n decides what language a page speaks.
//
// The chrome — navigation, buttons, labels, validation messages, empty states —
// follows the visitor. Product copy and the policy documents do not.
package i18n

import (
	"context"
	"net/http"
	"strings"
)

// Locale is a language goen speaks.
type Locale string

const (
	// ZhHant is Traditional Chinese, the language the content is authored in.
	ZhHant Locale = "zh-Hant"
	// En is English.
	En Locale = "en"
)

// Default is what a visitor gets who has expressed no preference.
const Default = ZhHant

// CookieName remembers a visitor's choice.
const CookieName = "__Host-goen_locale"

// insecureCookieName is the development name: a __Host- cookie is never sent
// back over plain http://, so a dev machine would forget the choice every request.
const insecureCookieName = "goen_locale"

// CookieMaxAge is a year. A language preference is not a session.
const CookieMaxAge = 365 * 24 * 60 * 60

// Known reports whether s names a locale goen speaks.
func Known(s string) bool {
	return Locale(s) == ZhHant || Locale(s) == En
}

// Parse turns a string into a locale, or returns Default.
func Parse(s string) Locale {
	if Known(s) {
		return Locale(s)
	}
	return Default
}

// Tag is the value for <html lang>.
func (l Locale) Tag() string { return string(l) }

// Label is what the switch calls this locale, in that locale.
func (l Locale) Label() string {
	if l == En {
		return "English"
	}
	return "繁體中文"
}

type localeKey struct{}

// WithLocale attaches a locale to a request context.
func WithLocale(ctx context.Context, l Locale) context.Context {
	return context.WithValue(ctx, localeKey{}, l)
}

// FromContext is the request's locale, or Default when nothing set one.
func FromContext(ctx context.Context) Locale {
	l, ok := ctx.Value(localeKey{}).(Locale)
	if !ok {
		return Default
	}
	return l
}

// StripeTag is the locale to hand Stripe's hosted Checkout. Stripe's tag for
// Traditional Chinese is zh-TW, not zh-Hant, so this is a mapping and not Tag().
func (l Locale) StripeTag() string {
	switch l {
	case ZhHant:
		return "zh-TW"
	case En:
		return "en"
	}
	panic("i18n: no Stripe locale for " + string(l))
}

// CookieNameFor is the cookie name for this deployment.
func CookieNameFor(secure bool) string {
	if secure {
		return CookieName
	}
	return insecureCookieName
}

// Detect works out what language to serve: the cookie wins, then Accept-Language.
func Detect(r *http.Request, secure bool) Locale {
	if c, err := r.Cookie(CookieNameFor(secure)); err == nil && Known(c.Value) {
		return Locale(c.Value)
	}
	return fromAcceptLanguage(r.Header.Get("Accept-Language"))
}

func fromAcceptLanguage(header string) Locale {
	for part := range strings.SplitSeq(header, ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		switch {
		case tag == "":
			continue
		case strings.HasPrefix(tag, "zh"):
			return ZhHant
		case strings.HasPrefix(tag, "en"):
			return En
		}
	}
	return Default
}

// SetCookie records a visitor's choice.
func SetCookie(w http.ResponseWriter, l Locale, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: dev-only opt-out, secure by default
		Name:     CookieNameFor(secure),
		Value:    string(l),
		Path:     "/",
		MaxAge:   CookieMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
