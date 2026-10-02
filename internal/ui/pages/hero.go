package pages

import (
	"strconv"

	"github.com/koopa0/goen/assets"
)

// CTA is a call to action: a label and where it goes, both or neither.
type CTA struct {
	Label string
	Href  string
}

// Shown reports whether there is a button to draw.
func (c CTA) Shown() bool { return c.Label != "" && c.Href != "" }

// SlideLayout is how a hero slide sets its photograph: filling the slide with
// the copy over its calm side, or beside the copy at its own proportions.
type SlideLayout string

// The two layouts. A campaign header and an uploaded slide image are wide
// photographs and fill the slide; a department photograph is 4:3 and sits
// beside its copy.
const (
	SlidePhoto SlideLayout = "photo"
	SlideSplit SlideLayout = "split"
)

// HeroSlide is one slide of the home page's carousel.
type HeroSlide struct {
	Layout SlideLayout
	Tone   Tone
	Photo  Photo
	// PhotoWidth and PhotoHeight are the photograph's intrinsic size, rendered
	// together because a width on its own reserves no space.
	PhotoWidth, PhotoHeight int
	Title                   string
	// Fact is the one muted line under the title; it states no discount.
	Fact string
	CTA  CTA
}

// PhotoSizes is the img sizes attribute for the layout: the slide's width for
// a photograph that fills it, the height-bound 4:3 box for one beside the copy.
func (s HeroSlide) PhotoSizes() string {
	if s.Layout == SlideSplit {
		return "(min-width: 1024px) 800px, 100vw"
	}
	return "100vw"
}

// HasPhotoSize reports whether both dimensions are known.
func (s HeroSlide) HasPhotoSize() bool { return s.PhotoWidth > 0 && s.PhotoHeight > 0 }

// PhotoWidthText and PhotoHeightText are the dimensions as attributes.
func (s HeroSlide) PhotoWidthText() string { return strconv.Itoa(s.PhotoWidth) }

func (s HeroSlide) PhotoHeightText() string { return strconv.Itoa(s.PhotoHeight) }

// AdminHeroSlide is one queued slide as the back office sees it.
type AdminHeroSlide struct {
	ID       string
	Eyebrow  string
	Headline string
	CTALabel string
	CTAHref  string
	ImageKey string
	Active   bool
	InWindow bool
	Position int32
	EndsAt   string
}

// Live reports whether a visitor could be seeing this one.
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

// Srcset offers the 400px rendition to the back office's 160px tile; the
// original upload is a multi-megabyte download for it.
func (s AdminHeroSlide) Srcset() string { return assets.UploadedRenditionSrcset(s.ImageKey, 400) }

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

// AdminHeroView is the queue page; it carries the promotional strip too.
type AdminHeroView struct {
	Rows   []AdminHeroSlide
	Notice string
	Errors map[string]string
	Draft  AdminHeroDraft

	Banners     []AdminBanner
	BannerDraft AdminBannerDraft
}

// AdminBanner is one promotional strip as the back office lists it.
type AdminBanner struct {
	ID         string
	Message    string
	Short      string
	Code       string
	CTALabel   string
	CTAHref    string
	MessageEn  string
	ShortEn    string
	CTALabelEn string
	Active     bool
	EndsAt     string
}

// Translated reports whether this strip reads in English; the message decides.
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
	Eyebrow, Headline, Body       string
	PrimaryLabel, PrimaryHref     string
	SecondLabel, SecondHref       string
	ImageKey, ImageAlt, Days      string
	EyebrowEn, HeadlineEn, BodyEn string
	PrimaryLabelEn, SecondLabelEn string
	ImageAltEn                    string
}

// Empty reports whether nothing is queued.
func (v *AdminHeroView) Empty() bool { return len(v.Rows) == 0 }

// Showing is the slide a visitor sees right now, or "" for the built-in copy.
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
