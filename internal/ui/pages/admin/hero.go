package admin

import (
	"context"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

type HeroSlide struct {
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
func (s HeroSlide) Live() bool { return s.Active && s.InWindow }

// State is the one word a staff member scans for.
func (s HeroSlide) State() string {
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
func (s HeroSlide) ImageURL() string {
	if s.ImageKey == "" {
		return ""
	}
	return "/media/" + s.ImageKey
}

// Srcset offers the 400px rendition to the back office's 160px tile; the
// original upload is a multi-megabyte download for it.
func (s HeroSlide) Srcset() string { return assets.UploadedRenditionSrcset(s.ImageKey, 400) }

// ToggleAction is where the on/off form posts.
func (s HeroSlide) ToggleAction() string { return "/admin/home/" + s.ID + "/active" }

// PromoteAction is where the make-current form posts.
func (s HeroSlide) PromoteAction() string { return "/admin/home/" + s.ID + "/promote" }

// NextActive is what the toggle would set it to.
func (s HeroSlide) NextActive() string {
	if s.Active {
		return "false"
	}
	return "true"
}

// ToggleLabel is what the button says.
func (s HeroSlide) ToggleLabel() string {
	if s.Active {
		return "停用" // i18n-exempt: back office, /admin/home
	}
	return "啟用" // i18n-exempt: back office, /admin/home
}

// HeroView is the queue page; it carries the promotional strip too.
type HeroView struct {
	Rows     []HeroSlide
	Carousel []pages.HeroSlide
	Notice   string
	Errors   map[string]string
	Draft    HeroDraft

	Banners     []Banner
	BannerDraft BannerDraft
}

// Banner is one promotional strip as the back office lists it.
type Banner struct {
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
func (b Banner) Translated() bool { return b.MessageEn != "" }

// HasCTA reports whether this strip carries a button.
func (b Banner) HasCTA() bool { return b.CTALabel != "" }

// Scheduled reports whether it has an end date.
func (b Banner) Scheduled() bool { return b.EndsAt != "" }

// BannerDraft carries a refused strip form's values back.
type BannerDraft struct {
	Message, Short, Code    string
	CTALabel, CTAHref, Days string
	MessageEn, ShortEn      string
	CTALabelEn              string
}

// HasBanners reports whether any promotion exists.
func (v *HeroView) HasBanners() bool { return len(v.Banners) > 0 }

// HeroDraft carries a refused form's values back.
type HeroDraft struct {
	Eyebrow, Headline, Body       string
	PrimaryLabel, PrimaryHref     string
	SecondLabel, SecondHref       string
	ImageKey, ImageAlt, Days      string
	EyebrowEn, HeadlineEn, BodyEn string
	PrimaryLabelEn, SecondLabelEn string
	ImageAltEn                    string
}

// Empty reports whether nothing is queued.
func (v *HeroView) Empty() bool { return len(v.Rows) == 0 }

func (v *HeroView) scheduledShown() int {
	n := 0
	for _, s := range v.Carousel {
		if s.Source == pages.SlideScheduled {
			n++
		}
	}
	return n
}

// IsShowing reports whether this queued slide is in the storefront's carousel:
// the carousel takes the first live ones in queue order, as many as it shows.
func (v *HeroView) IsShowing(s HeroSlide) bool {
	n := v.scheduledShown()
	for _, r := range v.Rows {
		if n == 0 {
			return false
		}
		if r.Live() {
			if r.ID == s.ID {
				return true
			}
			n--
		}
	}
	return false
}

// NoSlides reports whether the home page draws no carousel.
func (v *HeroView) NoSlides() bool { return len(v.Carousel) == 0 }

func SourceLabel(ctx context.Context, src pages.SlideSource) string {
	switch src {
	case pages.SlideScheduled:
		return i18n.T(ctx, i18n.KeyAdminHomeSourceScheduled)
	case pages.SlideCampaign:
		return i18n.T(ctx, i18n.KeyAdminHomeSourceCampaign)
	case pages.SlideDepartment:
		return i18n.T(ctx, i18n.KeyAdminHomeSourceDepartment)
	}
	panic("admin: unknown slide source " + string(src))
}

// HasErr reports whether a field was refused.
func (v *HeroView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

// Err is why.
func (v *HeroView) Err(f string) string { return v.Errors[f] }
