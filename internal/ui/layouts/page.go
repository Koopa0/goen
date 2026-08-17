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
	// Nav is the slug of the top-level category to mark as current.
	Nav string
	// StructuredData is a JSON-LD document; empty omits the script entirely.
	StructuredData string
}

// NavItem is one top-level category entry in the header.
type NavItem struct {
	Slug string
	Name string
	Href string
}

type topNavKey struct{}

// WithTopNav carries the header's category row down to the chrome. The row is
// read from the catalogue, never a hard-coded list whose comment claimed
// TestTopNavPointsAtRealCategories kept it honest. // named-test-exempt: this line RECORDS that the test was never written
func WithTopNav(ctx context.Context, items []NavItem) context.Context {
	return context.WithValue(ctx, topNavKey{}, items)
}

// TopNavFrom is the header's category row, or nothing when a page renders
// outside the middleware.
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

func (p Page) title(ctx context.Context) string {
	if p.Title == "" {
		return i18n.T(ctx, i18n.KeySiteTitle)
	}
	return p.Title + " · goen"
}

func (p Page) current(item NavItem) bool {
	return p.Nav != "" && p.Nav == item.Slug
}

func (p Page) ariaCurrent(item NavItem) string {
	if p.current(item) {
		return "page"
	}
	return "false"
}

func boolAttr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// cartLabel names the cart link for assistive technology; the badge is
// decorative and screen readers do not read it.
func cartLabel(ctx context.Context, count int) string {
	if count == 0 {
		return i18n.T(ctx, i18n.KeyCartEmpty)
	}
	// Substituted, not appended: the two languages put the count in different
	// places within the sentence.
	return strings.Replace(i18n.T(ctx, i18n.KeyCartCount), "%s", strconv.Itoa(count), 1)
}
