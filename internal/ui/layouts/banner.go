package layouts

import "context"

type Banner struct {
	ID           string
	Message      string
	MessageShort string
	Code         string
	CTALabel     string
	CTAHref      string
}

func (b Banner) Shown() bool { return b.Message != "" }

func (b Banner) HasCTA() bool { return b.CTALabel != "" && b.CTAHref != "" }

func (b Banner) HasCode() bool { return b.Code != "" }

func (b Banner) Narrow() string {
	if b.MessageShort != "" {
		return b.MessageShort
	}
	return b.Message
}

type bannerKey struct{}

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

func (b Banner) DismissAction() string { return "/promo/dismiss" }
