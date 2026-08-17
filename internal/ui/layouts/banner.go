package layouts

import "context"

// Banner is the site-wide promotional strip above the header.
type Banner struct {
	ID           string
	Message      string
	MessageShort string
	Code         string
	CTALabel     string
	CTAHref      string
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
