package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// CTA is a call to action: a label and where it goes.
//
// Both or neither. hero_slides_secondary_cta_complete holds that in the schema,
// so a button with no destination cannot be stored.
type CTA struct {
	Label string
	Href  string
}

// Shown reports whether there is a button to draw.
func (c CTA) Shown() bool { return c.Label != "" && c.Href != "" }

// Hero is the band at the top of the home page.
type Hero struct {
	Eyebrow      string
	Headline     string
	Body         string
	PrimaryCTA   CTA
	SecondaryCTA CTA
	// ImageKey is a media digest, or empty for the built-in artwork.
	ImageKey string
	ImageAlt string
	// ImageWidth is the original's width, which the srcset needs to state a
	// number that is true. Zero when the built-in image is used.
	ImageWidth int
}

// Custom reports whether this came from the database rather than the fallback.
func (h Hero) Custom() bool { return h.ImageKey != "" }

// DefaultHero is what the home page shows when nothing is scheduled.
//
// The copy that was in the template before hero_slides had any code. Keeping it
// here rather than seeding a row means an EMPTY TABLE IS A WORKING SITE:
// content management a shop must populate before its home page renders is a
// dependency, not a feature.
//
// Translated, unlike a slide the shop schedules. Copy compiled into the binary
// is goen's to say in both languages; copy typed into hero_slides is the shop's
// to say however it likes, and a promotion is authored content the way a product
// description is.
func DefaultHero(ctx context.Context) Hero {
	return Hero{
		Eyebrow:      i18n.T(ctx, i18n.KeyHeroEyebrow),
		Headline:     i18n.T(ctx, i18n.KeyHeroHeadline),
		Body:         i18n.T(ctx, i18n.KeyHeroBody),
		PrimaryCTA:   CTA{Label: i18n.T(ctx, i18n.KeyHeroPrimaryCTA), Href: "/deals"},
		SecondaryCTA: CTA{Label: i18n.T(ctx, i18n.KeyHeroSecondaryCTA), Href: "/about"},
	}
}

// AdminHeroSlide is one queued slide as the back office sees it.
type AdminHeroSlide struct {
	ID       string
	Eyebrow  string
	Headline string
	CTALabel string
	CTAHref  string
	ImageKey string
	Active   bool
	// InWindow is whether its schedule allows it right now. Separate from
	// Active for the same reason a coupon's is: a switched-on slide whose
	// window has passed is off to a visitor and on in a list that reads only
	// is_active.
	InWindow bool
	Position int32
	EndsAt   string
}

// Live reports whether a visitor could be seeing this one.
//
// Could, not is: only the FIRST slide that qualifies shows, and this type does
// not know its neighbours. AdminHeroView.Showing is what names the one.
func (s AdminHeroSlide) Live() bool { return s.Active && s.InWindow }

// State is the one word a staff member scans for.
func (s AdminHeroSlide) State() string {
	switch {
	case !s.Active:
		return "已停用" // i18n-exempt: back office, /admin/home
	case !s.InWindow:
		return "不在檔期內" // i18n-exempt: back office, /admin/home
	default:
		return "可顯示" // i18n-exempt: back office, /admin/home
	}
}

// ImageURL is where its artwork is served, or empty for the built-in one.
func (s AdminHeroSlide) ImageURL() string {
	if s.ImageKey == "" {
		return ""
	}
	return "/media/" + s.ImageKey
}

// ToggleAction is where the on/off form posts.
func (s AdminHeroSlide) ToggleAction() string { return "/admin/home/" + s.ID + "/active" }

// PromoteAction is where the make-current form posts.
func (s AdminHeroSlide) PromoteAction() string { return "/admin/home/" + s.ID + "/promote" }

// NextActive is what the toggle would set it to.
func (s AdminHeroSlide) NextActive() string {
	if s.Active {
		return "false"
	}
	return "true"
}

// ToggleLabel is what the button says.
func (s AdminHeroSlide) ToggleLabel() string {
	if s.Active {
		return "停用" // i18n-exempt: back office, /admin/home
	}
	return "啟用" // i18n-exempt: back office, /admin/home
}

// AdminHeroView is the queue page. It carries the promotional strip too: both are
// "what the storefront says about itself", and a shop editing one is usually about
// to look at the other.
type AdminHeroView struct {
	Rows   []AdminHeroSlide
	Notice string
	Errors map[string]string
	Draft  AdminHeroDraft
	// Banners is the promotional strip's list. It had no back-office page at all
	// until this field existed — the only way to run a promotion was SQL, which the
	// layout check's psql fixture quietly documented.
	Banners     []AdminBanner
	BannerDraft AdminBannerDraft
}

// AdminBanner is one promotional strip as the back office lists it.
type AdminBanner struct {
	ID       string
	Message  string
	Short    string
	Code     string
	CTALabel string
	CTAHref  string
	// The English strip, empty for what nobody has translated.
	MessageEn  string
	ShortEn    string
	CTALabelEn string
	Active     bool
	EndsAt     string
}

// Translated reports whether this strip reads in English. The MESSAGE decides: it
// is the strip, and a translated button under a Chinese sentence is not a
// translated promotion.
func (b AdminBanner) Translated() bool { return b.MessageEn != "" }

// HasCTA reports whether this strip carries a button.
func (b AdminBanner) HasCTA() bool { return b.CTALabel != "" }

// Scheduled reports whether it has an end date.
func (b AdminBanner) Scheduled() bool { return b.EndsAt != "" }

// AdminBannerDraft carries a refused strip form's values back.
type AdminBannerDraft struct {
	Message, Short, Code    string
	CTALabel, CTAHref, Days string
	MessageEn, ShortEn      string
	CTALabelEn              string
}

// HasBanners reports whether any promotion exists.
func (v *AdminHeroView) HasBanners() bool { return len(v.Banners) > 0 }

// AdminHeroDraft carries a refused form's values back.
type AdminHeroDraft struct {
	Eyebrow, Headline, Body   string
	PrimaryLabel, PrimaryHref string
	SecondLabel, SecondHref   string
	ImageKey, ImageAlt, Days  string
	// The English hero. No English href: a link goes to one page.
	EyebrowEn, HeadlineEn, BodyEn string
	PrimaryLabelEn, SecondLabelEn string
	ImageAltEn                    string
}

// Empty reports whether nothing is queued.
func (v *AdminHeroView) Empty() bool { return len(v.Rows) == 0 }

// Showing is the slide a visitor sees right now, or "" when the built-in copy
// is showing.
//
// The FIRST qualifying slide in queue order, which is exactly what
// CurrentHeroSlide's ORDER BY picks — computed the same way here so the page
// names the same one the storefront renders.
func (v *AdminHeroView) Showing() string {
	for _, s := range v.Rows {
		if s.Live() {
			return s.ID
		}
	}
	return ""
}

// UsingFallback reports whether the storefront is showing the built-in copy.
func (v *AdminHeroView) UsingFallback() bool { return v.Showing() == "" }

// HasErr reports whether a field was refused.
func (v *AdminHeroView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

// Err is why.
func (v *AdminHeroView) Err(f string) string { return v.Errors[f] }

// IsShowing reports whether this slide is the one.
func (v *AdminHeroView) IsShowing(s AdminHeroSlide) bool { return v.Showing() == s.ID }
