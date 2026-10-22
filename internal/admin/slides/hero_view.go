package slides

import (
	"context"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/widget"
)

type HeroSlide struct {
	ID       string
	Eyebrow  string
	Headline string
	CTALabel string
	CTAHref  string
	// ImageDigest is the slide's upload, empty when it has no photograph.
	ImageDigest string
	Active      bool
	InWindow    bool
	Position    int32
	EndsAt      string
}

func (s HeroSlide) State(ctx context.Context) string {
	switch {
	case !s.Active:
		return i18n.T(ctx, i18n.KeyAdminCampaignOff)
	case !s.InWindow:
		return i18n.T(ctx, i18n.KeyAdminCampaignOutside)
	default:
		return i18n.T(ctx, i18n.KeyAdminHomeEligible)
	}
}

func (s HeroSlide) ImageURL() string {
	if s.ImageDigest == "" {
		return ""
	}
	return assets.MediaURL(s.ImageDigest)
}

// Srcset offers the 400px rendition to the back office's 160px tile; the
// original upload is a multi-megabyte download for it.
func (s HeroSlide) Srcset() string { return assets.UploadedRenditionSrcset(s.ImageDigest, 400) }

func (s HeroSlide) ToggleAction() string { return "/admin/slides/" + s.ID + "/active" }

func (s HeroSlide) PromoteAction() string { return "/admin/slides/" + s.ID + "/promote" }

func (s HeroSlide) NextActive() string {
	if s.Active {
		return "false"
	}
	return "true"
}

func (s HeroSlide) ToggleLabel(ctx context.Context) string {
	return widget.ToggleLabel(ctx, s.Active)
}

type HeroView struct {
	Bound    keyset.Bound
	Rows     []HeroSlide
	Carousel []hero.Slide
	Notice   widget.Result
	Errors   map[string]string
	Draft    HeroDraft
}

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
		if shown.Source == hero.SourceScheduled && shown.ID == s.ID {
			return true
		}
	}
	return false
}

func (v *HeroView) NoSlides() bool { return len(v.Carousel) == 0 }

func SourceLabel(ctx context.Context, src hero.Source) string {
	switch src {
	case hero.SourceScheduled:
		return i18n.T(ctx, i18n.KeyAdminHomeSourceScheduled)
	case hero.SourceCampaign:
		return i18n.T(ctx, i18n.KeyAdminHomeSourceCampaign)
	case hero.SourceDepartment:
		return i18n.T(ctx, i18n.KeyAdminHomeSourceDepartment)
	}
	return i18n.T(ctx, i18n.KeyAdminHomeSourceOther)
}

func (v *HeroView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

func (v *HeroView) Err(f string) string { return v.Errors[f] }
