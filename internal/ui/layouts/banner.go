package layouts

import "context"

// Banner is the site-wide promotional strip above the header.
type Banner struct {
	// ID is what a dismissal is keyed on, so the next promotion reappears for
	// somebody who closed this one.
	ID string
	// Message is the wide copy; MessageShort is different copy for a narrow
	// screen rather than a truncation of it.
	Message      string
	MessageShort string
	// Code is a discount code, rendered as a selectable chip.
	Code string
	// CTALabel and CTAHref are both or neither.
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

type bannerKey struct{}

// WithBanner attaches the banner a request should render.
func WithBanner(ctx context.Context, banner Banner) context.Context {
	return context.WithValue(ctx, bannerKey{}, banner)
}

func bannerFrom(ctx context.Context) Banner {
	b, ok := ctx.Value(bannerKey{}).(Banner)
	if !ok {
		return Banner{}
	}
	return b
}

// DismissAction is where the close button posts.
func (b Banner) DismissAction() string { return "/promo/dismiss" }
