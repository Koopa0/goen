package layouts

import "context"

// Banner is the site-wide promotional strip above the header.
//
// # Where it appears, and why not everywhere
//
// The storefront only. Checkout never shows it: that page has one job and an
// offer beside it is a conversion risk — and the discount field is already in
// the order summary. The account pages and the back office never show it
// either, because somebody there came to do a task, not to buy.
//
// # Why it is not sticky
//
// A promotion is a sentence you read on arrival, not navigation. Sticky, it
// would levy a permanent 44px tax on every screen for a message somebody has
// already read. It scrolls away and the header — which IS navigation — is what
// stays.
type Banner struct {
	// ID is what a dismissal is keyed on. Dismissing THIS banner hides this
	// promotion and nothing else: when the shop runs the next one the id
	// changes and it reappears, which is correct — a new promotion is new
	// information.
	ID string
	// Message is the wide copy. Short is different copy for a narrow screen,
	// not a truncation, which is why the column exists rather than the template
	// cutting the long one.
	Message      string
	MessageShort string
	// Code is a discount code, rendered as a selectable chip. Empty for a
	// promotion that needs none.
	Code string
	// CTALabel and CTAHref are both or neither — promo_banners_cta_complete
	// holds that in the schema.
	CTALabel string
	CTAHref  string
}

// Shown reports whether there is a promotion to draw.
func (b Banner) Shown() bool { return b.Message != "" }

// HasCTA reports whether it links somewhere.
func (b Banner) HasCTA() bool { return b.CTALabel != "" && b.CTAHref != "" }

// HasCode reports whether it carries a discount code.
func (b Banner) HasCode() bool { return b.Code != "" }

// Narrow is what a small screen shows.
func (b Banner) Narrow() string {
	if b.MessageShort != "" {
		return b.MessageShort
	}
	return b.Message
}

// bannerKey carries the strip to the document shell.
type bannerKey struct{}

// WithBanner attaches the banner a request should render.
//
// A context value rather than a field on every page's view model, for the
// reason CartCount is one: a field each handler must remember to fill is a
// field that goes unfilled, and layouts.Page.CartCount demonstrated exactly
// that — the badge read 0 for every visitor with a full cart until it moved
// here.
func WithBanner(ctx context.Context, banner Banner) context.Context {
	return context.WithValue(ctx, bannerKey{}, banner)
}

// bannerFrom is the banner to render, or the zero value for a page that shows
// none — which is every page outside the storefront.
func bannerFrom(ctx context.Context) Banner {
	b, ok := ctx.Value(bannerKey{}).(Banner)
	if !ok {
		return Banner{}
	}
	return b
}

// DismissAction is where the close button posts.
func (b Banner) DismissAction() string { return "/promo/dismiss" }
