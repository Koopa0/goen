// Package layouts holds goen's shared page chrome.
package layouts

import (
	"context"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

// htmxConfig is set from the document because a policy that allows no inline script
// leaves nowhere else. includeIndicatorCSS is off because htmx would otherwise adopt
// a stylesheet of its own, and every rule belongs in the sheet the policy allows.
const htmxConfig = `{"transitions":true,"includeIndicatorCSS":false}`

type Page struct {
	Title          string
	Description    string
	Nav            string
	StructuredData string
	SearchQuery    string
	// Newsletter repopulates the footer after a plain newsletter response;
	// its zero value is the normal empty subscription form.
	Newsletter NewsletterState
	// Share's zero value keeps the default preview picture.
	Share ShareImage
	// Await is the element the first paint waits for, so the script that names
	// a photo for the page transition finds it at the reveal.
	Await Await
}

// Await names an element a page holds its first paint for.
type Await uint8

const (
	AwaitNone Await = iota
	AwaitGallery
	AwaitPageheadPhoto
)

// ID is the element's id, or "" for AwaitNone.
func (a Await) ID() string {
	switch a {
	case AwaitNone:
		return ""
	case AwaitGallery:
		return "gallery"
	case AwaitPageheadPhoto:
		return "pagehead-photo"
	}
	return ""
}

// ShareImage has a site-relative Path because the head prefixes the configured origin,
// which a crawler needs to fetch it. Width and Height are 0 where unknown and are then
// left out of the tags rather than stated wrong.
type ShareImage struct {
	Path          string
	Width, Height int32
	Alt           string
}

type NavItem struct {
	Slug string
	Name string
	Href string
	// ProductCount is how many active products the department holds across its
	// sub-categories; 0 is left unprinted.
	ProductCount int
	Children     []NavItem
	Picks        []NavPick
}

// NavPick carries a Price that is already rendered, "from" included where variants differ: the
// chrome prints it as given.
type NavPick struct {
	Slug        string
	Name        string
	Price       string
	ImageURL    string
	ImageSrcset string
}

type topNavKey struct{}

// WithTopNav carries the header's category row, read from the catalogue.
func WithTopNav(ctx context.Context, items []NavItem) context.Context {
	return context.WithValue(ctx, topNavKey{}, items)
}

func TopNavFrom(ctx context.Context) []NavItem {
	items, ok := ctx.Value(topNavKey{}).([]NavItem)
	if !ok {
		return nil
	}
	return items
}

type dealsKey struct{}

// WithDeals says whether the deals page has something to buy. It is set in middleware
// beside the department row, which it shares a row with.
func WithDeals(ctx context.Context, open bool) context.Context {
	return context.WithValue(ctx, dealsKey{}, open)
}

// HasDeals is false outside the middleware: a link to a page with nothing on it is the
// worse mistake.
func HasDeals(ctx context.Context) bool {
	open, ok := ctx.Value(dealsKey{}).(bool)
	return ok && open
}

// departmentRow reports whether the second row has anything to say: a single
// department with no deals is the whole shop, and a row with one link in it is noise.
func departmentRow(ctx context.Context) bool {
	return len(TopNavFrom(ctx)) > 1 || HasDeals(ctx)
}

type originKey struct{}

// WithSiteOrigin carries scheme and host only, set by middleware from the configured
// base URL and never from the request's Host header, which a client chooses.
func WithSiteOrigin(ctx context.Context, origin string) context.Context {
	return context.WithValue(ctx, originKey{}, origin)
}

func SiteOrigin(ctx context.Context) string {
	if origin, ok := ctx.Value(originKey{}).(string); ok {
		return origin
	}
	return ""
}

type pathKey struct{}

// WithRequestPath carries the path without its query, for og:url.
func WithRequestPath(ctx context.Context, escapedPath string) context.Context {
	return context.WithValue(ctx, pathKey{}, escapedPath)
}

func RequestPath(ctx context.Context) string {
	if path, ok := ctx.Value(pathKey{}).(string); ok && path != "" {
		return path
	}
	return "/"
}

type staffKey struct{}

// WithStaff is set in middleware: a chrome fact each handler has to remember to fill
// goes unfilled.
func WithStaff(ctx context.Context, staff bool) context.Context {
	return context.WithValue(ctx, staffKey{}, staff)
}

// IsStaff is false outside the middleware, so a customer is never shown a door that answers 404.
func IsStaff(ctx context.Context) bool {
	staff, ok := ctx.Value(staffKey{}).(bool)
	return ok && staff
}

type footerLink struct {
	Key  i18n.Key
	Href string
}

func (l footerLink) Label(ctx context.Context) string { return i18n.T(ctx, l.Key) }

var (
	footerHelp = [...]footerLink{
		{Key: i18n.KeyContact, Href: "/contact"},
		{Key: i18n.KeyFAQ, Href: "/faq"},
		{Key: i18n.KeyShippingPolicy, Href: "/shipping"},
		{Key: i18n.KeyPaymentPolicy, Href: "/payment"},
		{Key: i18n.KeyReturnsPolicy, Href: "/returns"},
		{Key: i18n.KeyWarrantyPolicy, Href: "/warranty"},
	}
	footerAbout = [...]footerLink{
		{Key: i18n.KeyFooterAbout, Href: "/about"},
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

func currentPage(ctx context.Context, path string) bool {
	requestPath, _, _ := strings.Cut(web.RequestPath(ctx), "?")
	return requestPath == path
}

func cartLabel(ctx context.Context, count int) string {
	if count == 0 {
		return i18n.T(ctx, i18n.KeyCartEmpty)
	}
	// Substituted, not appended: the two languages put the count in different places.
	return i18n.Count(ctx, i18n.KeyCartCount, int64(count), strconv.Itoa(count))
}

// languageShown is the language the storefront's switch names: the one a click changes
// to. The back office's bar names the one that is on.
func languageShown(current i18n.Locale, showOther bool) i18n.Locale {
	if !showOther {
		return current
	}
	for _, l := range i18n.Locales() {
		if l != current {
			return l
		}
	}
	return current
}
