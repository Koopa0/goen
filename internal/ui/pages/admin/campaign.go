package admin

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

type CampaignRow struct {
	Slug         string
	Title        string
	Products     int64
	Active       bool
	Running      bool
	Sellable     bool
	StartsAtText string
	EndsAtText   string
}

func (c CampaignRow) State(ctx context.Context) string {
	return campaignState(ctx, c.Active, c.Running, c.Sellable)
}

func campaignState(ctx context.Context, active, running, sellable bool) string {
	switch {
	case !active:
		return i18n.T(ctx, i18n.KeyAdminCampaignOff)
	case !running:
		return i18n.T(ctx, i18n.KeyAdminCampaignOutside)
	case !sellable:
		return i18n.T(ctx, i18n.KeyAdminCampaignHidden)
	default:
		return i18n.T(ctx, i18n.KeyAdminCampaignRunning)
	}
}

func nextActive(active bool) string { return strconv.FormatBool(!active) }

func toggleLabel(ctx context.Context, active bool) string {
	if active {
		return i18n.T(ctx, i18n.KeyAdminToggleOff)
	}
	return i18n.T(ctx, i18n.KeyAdminToggleOn)
}

func (c CampaignRow) Live() bool { return c.Running && c.Sellable }

func (c CampaignRow) ProductsText() string { return strconv.FormatInt(c.Products, 10) }

func (c CampaignRow) Href() string { return "/admin/campaigns/" + c.Slug }

func (c CampaignRow) ToggleAction() string { return "/admin/campaigns/" + c.Slug + "/active" }

func (c CampaignRow) NextActive() string { return nextActive(c.Active) }

func (c CampaignRow) ToggleLabel(ctx context.Context) string { return toggleLabel(ctx, c.Active) }

type CampaignProduct struct {
	Slug string
	Name string
}

type CampaignsView struct {
	web.Bound

	Rows   []CampaignRow
	Notice components.Result
	Errors map[string]string
	Draft  CampaignDraft
}

type CampaignDraft struct {
	Slug    string
	Title   string
	TitleEn string
	Days    string
	Tone    string
}

func (d CampaignDraft) ToneOrStone() pages.Tone { return pages.ResolveTone(d.Tone) }

func (v CampaignsView) Empty() bool { return len(v.Rows) == 0 }

func (v CampaignsView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

func (v CampaignsView) Err(f string) string { return v.Errors[f] }

// CampaignDetail is what the edit page reads of a campaign. The dates are
// datetime-local field values, not display text.
type CampaignDetail struct {
	Title                      string
	StartsAtInput, EndsAtInput string
	Active, Running, Sellable  bool
}

type CampaignView struct {
	CampaignDetail

	ImageAltDraft   string
	ImageAltEnDraft string

	Slug     string
	Term     string
	Matches  []CampaignProduct
	Products []CampaignProduct
	Notice   components.Result
	Image    Header
	Tone     string
	Errors   map[string]string
}

type Header struct {
	Key   string
	Alt   string
	AltEn string
	Width int32
}

func (i Header) Has() bool { return i.Key != "" }

func (i Header) URL() string { return assets.ProductImageURL(i.Key) }

func (i Header) Srcset() string { return assets.ProductImageSrcsetAt(i.Key, int(i.Width)) }

func (v *CampaignView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

func (v *CampaignView) Err(f string) string { return v.Errors[f] }

func (v *CampaignView) ToneAction() string { return "/admin/campaigns/" + v.Slug + "/tone" }

func (v *CampaignView) ImageAction() string { return "/admin/campaigns/" + v.Slug + "/image" }

func (v *CampaignView) ImageRemoveAction() string {
	return "/admin/campaigns/" + v.Slug + "/image/remove"
}

func (v *CampaignView) WindowAction() string { return "/admin/campaigns/" + v.Slug + "/window" }

func (v *CampaignView) ActiveAction() string { return "/admin/campaigns/" + v.Slug + "/active" }

func (v *CampaignView) StateText(ctx context.Context) string {
	return campaignState(ctx, v.Active, v.Running, v.Sellable)
}

func (v *CampaignView) ToggleLabel(ctx context.Context) string { return toggleLabel(ctx, v.Active) }

func (v *CampaignView) NextActive() string { return nextActive(v.Active) }

func (v *CampaignView) Searching() bool { return v.Term != "" }

func (v *CampaignView) Empty() bool { return len(v.Products) == 0 }

func (v *CampaignView) FeatureAction() string { return "/admin/campaigns/" + v.Slug + "/products" }
