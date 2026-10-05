package admin

import (
	"context"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
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

func (s HeroSlide) ImageURL() string {
	if s.ImageKey == "" {
		return ""
	}
	return "/media/" + s.ImageKey
}

// Srcset offers the 400px rendition to the back office's 160px tile; the
// original upload is a multi-megabyte download for it.
func (s HeroSlide) Srcset() string { return assets.UploadedRenditionSrcset(s.ImageKey, 400) }

func (s HeroSlide) ToggleAction() string { return "/admin/home/" + s.ID + "/active" }

func (s HeroSlide) PromoteAction() string { return "/admin/home/" + s.ID + "/promote" }

func (s HeroSlide) NextActive() string {
	if s.Active {
		return "false"
	}
	return "true"
}

func (s HeroSlide) ToggleLabel() string {
	if s.Active {
		return "停用" // i18n-exempt: back office, /admin/home
	}
	return "啟用" // i18n-exempt: back office, /admin/home
}

type HeroView struct {
	Bound    web.Bound
	Rows     []HeroSlide
	Carousel []pages.HeroSlide
	Notice   string
	Errors   map[string]string
	Draft    HeroDraft

	BannerBound web.Bound
	Banners     []Banner
	BannerDraft BannerDraft
}

type BannersView struct {
	Rows  []Banner
	Bound web.Bound
}

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

func (b Banner) Translated() bool { return b.MessageEn != "" }

func (b Banner) HasCTA() bool { return b.CTALabel != "" }

func (b Banner) Scheduled() bool { return b.EndsAt != "" }

type BannerDraft struct {
	Message, Short, Code    string
	CTALabel, CTAHref, Days string
	MessageEn, ShortEn      string
	CTALabelEn              string
}

// BannerInEnglish is whether the banner form opens on its English fields: only
// when every refusal is in them, so no refused field starts hidden.
func (v *HeroView) BannerInEnglish() bool {
	if v.HasErr("message") || v.HasErr("short") {
		return false
	}
	return v.HasErr("message_en") || v.HasErr("short_en")
}

func (v *HeroView) HasBanners() bool { return len(v.Banners) > 0 }

type HeroDraft struct {
	Eyebrow, Headline, Body       string
	PrimaryLabel, PrimaryHref     string
	SecondLabel, SecondHref       string
	ImageKey, ImageAlt, Days      string
	EyebrowEn, HeadlineEn, BodyEn string
	PrimaryLabelEn, SecondLabelEn string
	ImageAltEn                    string
}

func (v *HeroView) Empty() bool { return len(v.Rows) == 0 }

// IsShowing compares with the storefront's identities, independently of this
// management page's position in the schedule.
func (v *HeroView) IsShowing(s HeroSlide) bool {
	if s.ID == "" {
		return false
	}
	for i := range v.Carousel {
		shown := &v.Carousel[i]
		if shown.Source == pages.SlideScheduled && shown.ID == s.ID {
			return true
		}
	}
	return false
}

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
	return i18n.T(ctx, i18n.KeyAdminHomeSourceOther)
}

func (v *HeroView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

func (v *HeroView) Err(f string) string { return v.Errors[f] }
