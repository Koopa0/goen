package admin

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// CampaignRow is one promotion in the campaigns list.
type CampaignRow struct {
	Slug         string
	Title        string
	Products     int64
	Active       bool
	Running      bool
	StartsAtText string
	EndsAtText   string
}

// State is the word a staff member scans for.
func (c CampaignRow) State(ctx context.Context) string {
	return campaignState(ctx, c.Active, c.Running, c.Products)
}

// campaignState says where a campaign stands; active and running differ.
func campaignState(ctx context.Context, active, running bool, products int64) string {
	switch {
	case !active:
		return i18n.T(ctx, i18n.KeyAdminCampaignOff)
	case !running:
		return i18n.T(ctx, i18n.KeyAdminCampaignOutside)
	case products == 0:
		return i18n.T(ctx, i18n.KeyAdminCampaignEmpty)
	default:
		return i18n.T(ctx, i18n.KeyAdminCampaignRunning)
	}
}

// nextActive is what the on/off form sets, as a form value.
func nextActive(active bool) string { return strconv.FormatBool(!active) }

func toggleLabel(ctx context.Context, active bool) string {
	if active {
		return i18n.T(ctx, i18n.KeyAdminToggleOff)
	}
	return i18n.T(ctx, i18n.KeyAdminToggleOn)
}

// Live reports whether a shopper can see it with something on it.
func (c CampaignRow) Live() bool { return c.Running && c.Products > 0 }

// ProductsText is how many it features.
func (c CampaignRow) ProductsText() string { return strconv.FormatInt(c.Products, 10) }

// Href is its edit page.
func (c CampaignRow) Href() string { return "/admin/campaigns/" + c.Slug }

// ToggleAction is where the on/off form posts.
func (c CampaignRow) ToggleAction() string { return "/admin/campaigns/" + c.Slug + "/active" }

// NextActive is what the on/off form sets.
func (c CampaignRow) NextActive() string { return nextActive(c.Active) }

// ToggleLabel is what the on/off button says.
func (c CampaignRow) ToggleLabel(ctx context.Context) string { return toggleLabel(ctx, c.Active) }

// CampaignProduct is one featured product.
type CampaignProduct struct {
	Slug string
	Name string
}

// CampaignsView is the promotions page.
type CampaignsView struct {
	pages.ListBound

	Rows   []CampaignRow
	Notice string
	Errors map[string]string
	Draft  CampaignDraft
}

// CampaignDraft carries a refused form's values back.
type CampaignDraft struct {
	Slug    string
	Title   string
	TitleEn string
	Days    string
	Tone    string
}

// ToneOrStone is the tone to preselect: the refused draft's, or stone.
func (d CampaignDraft) ToneOrStone() pages.Tone { return pages.ResolveTone(d.Tone) }

// Empty reports whether nothing has been created.
func (v CampaignsView) Empty() bool { return len(v.Rows) == 0 }

// HasErr reports whether a field was refused.
func (v CampaignsView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

// Err is why.
func (v CampaignsView) Err(f string) string { return v.Errors[f] }

// CampaignDetail is what the edit page reads of a campaign. The dates are
// datetime-local field values, not display text.
type CampaignDetail struct {
	Title                      string
	StartsAtInput, EndsAtInput string
	Active, Running            bool
}

// CampaignView is one campaign's edit page.
type CampaignView struct {
	CampaignDetail

	Slug     string
	Term     string
	Matches  []CampaignProduct
	Products []CampaignProduct
	Notice   string
	Image    Header
	Tone     string
	// Errors names the fields a refused image form got wrong, by field name.
	Errors map[string]string
}

// Header is the header photograph a campaign or a category has now.
type Header struct {
	Key   string
	Alt   string
	AltEn string
	Width int32
}

// Has reports whether there is a header.
func (i Header) Has() bool { return i.Key != "" }

// URL is where the header is served, or "" for a key that names nothing.
func (i Header) URL() string { return assets.ProductImageURL(i.Key) }

// Srcset offers the smaller renditions to the edit page's preview tile.
func (i Header) Srcset() string { return assets.ProductImageSrcsetAt(i.Key, int(i.Width)) }

// HasErr reports whether a field of the image form was refused.
func (v *CampaignView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

// Err is why.
func (v *CampaignView) Err(f string) string { return v.Errors[f] }

// ToneAction is where the tone form posts.
func (v *CampaignView) ToneAction() string { return "/admin/campaigns/" + v.Slug + "/tone" }

// ImageAction is where the header upload form posts.
func (v *CampaignView) ImageAction() string { return "/admin/campaigns/" + v.Slug + "/image" }

// ImageRemoveAction is where the remove form posts.
func (v *CampaignView) ImageRemoveAction() string {
	return "/admin/campaigns/" + v.Slug + "/image/remove"
}

func (v *CampaignView) WindowAction() string { return "/admin/campaigns/" + v.Slug + "/window" }

// ActiveAction is where the on/off form posts.
func (v *CampaignView) ActiveAction() string { return "/admin/campaigns/" + v.Slug + "/active" }

// StateText is the word the list uses for where the campaign stands.
func (v *CampaignView) StateText(ctx context.Context) string {
	return campaignState(ctx, v.Active, v.Running, int64(len(v.Products)))
}

// ToggleLabel is what the on/off button says.
func (v *CampaignView) ToggleLabel(ctx context.Context) string { return toggleLabel(ctx, v.Active) }

// NextActive is what the on/off form sets.
func (v *CampaignView) NextActive() string { return nextActive(v.Active) }

func (v *CampaignView) Searching() bool { return v.Term != "" }

// Empty reports whether it features nothing.
func (v *CampaignView) Empty() bool { return len(v.Products) == 0 }

// FeatureAction is where the add-product form posts.
func (v *CampaignView) FeatureAction() string { return "/admin/campaigns/" + v.Slug + "/products" }
