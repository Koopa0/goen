// Package layouts renders goen's shared page chrome: the document shell, the
// site header and the site footer. Pages compose their own content inside
// [Base] and never render <html>, <head>, the header or the footer themselves.
package layouts

import (
	"context"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// Page is the chrome-level view model every goen page supplies.
type Page struct {
	// Title is the page title without the site suffix.
	Title string
	// Description fills the meta description tag; empty omits the tag.
	Description string
	// Nav is the slug of the top-level category to mark as current, or "" for
	// pages that sit outside the category tree.
	Nav string
	// StructuredData is a JSON-LD document describing what this page is, for a
	// search engine. Empty omits the script entirely.
	//
	// It is a string rather than a struct because each page type describes a
	// different schema.org thing, and a union type covering all of them would
	// be a type nobody reads. The page that knows what it is builds it.
	StructuredData string
}

// NavItem is one top-level category entry in the header.
type NavItem struct {
	Slug string
	Name string
	Href string
}

// topNavKey is unexported so nothing outside this package can put a value under
// it, which is what keeps [TopNavFrom] honest about where the nav came from.
type topNavKey struct{}

// WithTopNav carries the header's category row down to the chrome.
//
// The row is read from the CATALOGUE and travels in the context, rather than
// sitting in a package-level var of hard-coded NavItems. A hard-coded list is
// wrong three ways at once:
//
//   - the names are Chinese for every visitor, on the grounds that a category
//     name is CONTENT. It is not: it is the most-read chrome on the site, in the
//     header of every page, so an English visitor meets a Chinese navigation bar
//     on a page whose every other word has been translated.
//   - it is a second copy of `categories`, and the comment over goen's own list
//     named TestTopNavPointsAtRealCategories as what kept the two from drifting. // named-test-exempt: this line RECORDS that the test was never written
//     That test was never written — the third claim of enforcement this project
//     has found with nothing behind it.
//   - a slug that stops resolving is a dead link on every page at once, which is
//     what 耳機 became when it pointed at /c/headphones while the category was
//     audio.
//
// Reading the catalogue removes all three: there is one name for a category, one
// place it is translated, and a link cannot point at something that is not there.
func WithTopNav(ctx context.Context, items []NavItem) context.Context {
	return context.WithValue(ctx, topNavKey{}, items)
}

// TopNavFrom is the header's category row, or nothing when a page renders
// outside the middleware. Nothing renders as no category links rather than as a
// panic: losing the nav row is far smaller than losing the page.
func TopNavFrom(ctx context.Context) []NavItem {
	items, ok := ctx.Value(topNavKey{}).([]NavItem)
	if !ok {
		return nil
	}
	return items
}

// FooterLink is one entry in a footer link column.
type FooterLink struct {
	Key  i18n.Key
	Href string
}

// Label is the link's text in the request's language.
func (l FooterLink) Label(ctx context.Context) string { return i18n.T(ctx, l.Key) }

// FooterShopping and FooterAbout are the footer's two link columns.
//
// The label is a KEY rather than a string: the footer is chrome, so it follows
// the language, and a package-level slice of literals could not.
var (
	FooterShopping = []FooterLink{
		{Key: i18n.KeyShippingPolicy, Href: "/shipping"},
		{Key: i18n.KeyPaymentPolicy, Href: "/payment"},
		{Key: i18n.KeyReturnsPolicy, Href: "/returns"},
		{Key: i18n.KeyWarrantyPolicy, Href: "/warranty"},
	}
	FooterAbout = []FooterLink{
		{Key: i18n.KeyFooterAbout, Href: "/about"},
		{Key: i18n.KeyContact, Href: "/contact"},
		{Key: i18n.KeyFAQ, Href: "/faq"},
		{Key: i18n.KeyTermsPolicy, Href: "/terms"},
		{Key: i18n.KeyPrivacyPolicy, Href: "/privacy"},
	}
)

// title composes the document title. A page without its own title gets the
// site's own name rather than a stray separator.
func (p Page) title(ctx context.Context) string {
	if p.Title == "" {
		return i18n.T(ctx, i18n.KeySiteTitle)
	}
	return p.Title + " · goen"
}

// current reports whether item is the page's active navigation entry. It is
// false for an empty slug so an unrelated page never highlights a category.
func (p Page) current(item NavItem) bool {
	return p.Nav != "" && p.Nav == item.Slug
}

// ariaCurrent renders the aria-current value for a navigation entry.
func (p Page) ariaCurrent(item NavItem) string {
	if p.current(item) {
		return "page"
	}
	return "false"
}

// boolAttr renders a Go bool as the literal an ARIA state attribute expects.
func boolAttr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// cartLabel names the cart link for assistive technology, which cannot read
// the count badge because the badge is decorative.
func cartLabel(ctx context.Context, count int) string {
	if count == 0 {
		return i18n.T(ctx, i18n.KeyCartEmpty)
	}
	// The count is substituted rather than concatenated, because the two
	// languages put it in different places: "購物車,3 件商品" against
	// "Cart, 3 items". A template that appended it would read correctly in one
	// and not the other.
	return strings.Replace(i18n.T(ctx, i18n.KeyCartCount), "%s", strconv.Itoa(count), 1)
}
