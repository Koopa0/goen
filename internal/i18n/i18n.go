// Package i18n decides what language a page speaks.
//
// # What is translated and what is not
//
// The CHROME is: navigation, buttons, labels, validation messages, empty
// states — everything the code writes. Product names, descriptions and the
// policy documents are NOT: those are content, and translating them is an
// editorial job that belongs to whoever writes them, not to a lookup table.
//
// That split is stated on the page rather than hidden. A visitor who switches
// to English and finds the buttons in English and the product copy in Chinese
// has been told what to expect; one who finds a machine-translated product
// description has been misled about what the shop knows.
//
// # Why not a library
//
// goen has two locales and a few hundred strings. A catalogue and a lookup are
// the whole job; golang.org/x/text/message would bring plural rules and
// CLDR data for a shop that pluralises nothing — 中文 has no plural form and
// the English side is UI chrome with fixed counts beside it.
package i18n

import (
	"context"
	"net/http"
	"strings"
)

// Locale is a language goen speaks.
type Locale string

// The locales, and the tags they become in <html lang>.
const (
	// ZhHant is Traditional Chinese, the default and the language the content
	// is authored in.
	ZhHant Locale = "zh-Hant"
	// En is English.
	En Locale = "en"
)

// Default is what a visitor gets who has expressed no preference.
//
// Traditional Chinese, because that is what the catalogue is written in and
// what every product description is. A visitor from anywhere lands on a
// coherent page and can switch.
const Default = ZhHant

// CookieName remembers a visitor's choice.
//
// The server reads it and stamps the language onto <html lang> before the first
// byte, so the page never renders in one language and then changes — which is
// what a client-side switch does, and what makes a locale toggle feel broken.
const CookieName = "__Host-goen_locale"

// insecureCookieName is the development name. A __Host- cookie is never sent
// back over plain http://, so a dev machine would appear to forget the choice
// on every request.
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

// Label is what the switch calls this locale, in that locale — a visitor
// looking for their own language should not have to read another one to find
// it.
func (l Locale) Label() string {
	if l == En {
		return "English"
	}
	return "繁體中文"
}

// localeKey is unexported so nothing outside this package can put a value under
// it.
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

// StripeTag is the locale to hand Stripe's hosted Checkout.
//
// Stripe renders a page goen does not control, and it takes a locale of its own.
// It was pinned to zh-TW, so an English visitor filled in an English form,
// pressed an English button and landed on a Chinese payment page — the one page
// in the flow where being unsure what you are agreeing to matters most.
//
// Stripe's tag for Traditional Chinese is zh-TW, not zh-Hant, which is why this
// is a mapping rather than Tag().
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

// Detect works out what language to serve.
//
// The cookie wins, because it is an explicit choice. Accept-Language is a hint
// from a browser the visitor may never have configured, so it only decides for
// somebody who has not chosen — and it is read leniently, because a header that
// does not parse should mean "no preference", never an error page.
func Detect(r *http.Request, secure bool) Locale {
	if c, err := r.Cookie(CookieNameFor(secure)); err == nil && Known(c.Value) {
		return Locale(c.Value)
	}
	return fromAcceptLanguage(r.Header.Get("Accept-Language"))
}

// fromAcceptLanguage picks a locale from the header.
//
// Quality values are ignored on purpose: with two locales and Chinese as the
// default, the only question is whether English appears BEFORE any Chinese
// variant. Parsing q-values to answer that would be arithmetic in service of a
// decision it cannot change.
func fromAcceptLanguage(header string) Locale {
	for _, part := range strings.Split(header, ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		switch {
		case tag == "":
			continue
		case strings.HasPrefix(tag, "zh"):
			// Any Chinese, including zh-CN: a Simplified reader is far better
			// served by Traditional than by English.
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
