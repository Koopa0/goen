package banners

import (
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/components"
)

type BannersView struct {
	Rows   []Banner
	Bound  keyset.Bound
	Notice components.Result
	Errors map[string]string
	Draft  BannerDraft
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

// InEnglish is whether the banner form opens on its English fields: only
// when every refusal is in them, so no refused field starts hidden.
func (v *BannersView) InEnglish() bool {
	if v.HasErr("message") || v.HasErr("short") {
		return false
	}
	return v.HasErr("message_en") || v.HasErr("short_en")
}

func (v *BannersView) HasBanners() bool { return len(v.Rows) > 0 }

func (v *BannersView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

func (v *BannersView) Err(f string) string { return v.Errors[f] }
