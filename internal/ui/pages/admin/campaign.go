package admin

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Campaign is one promotion as the back office sees it.
type Campaign struct {
	Slug     string
	Title    string
	Products int64
	Active   bool
	Running  bool
	StartsAt string
	EndsAt   string
}

// State is the word a staff member scans for; active and running differ.
func (c Campaign) State(ctx context.Context) string {
	switch {
	case !c.Active:
		return i18n.T(ctx, i18n.KeyAdminCampaignOff)
	case !c.Running:
		return i18n.T(ctx, i18n.KeyAdminCampaignOutside)
	case c.Products == 0:
		return i18n.T(ctx, i18n.KeyAdminCampaignEmpty)
	default:
		return i18n.T(ctx, i18n.KeyAdminCampaignRunning)
	}
}

// Live reports whether a shopper can see it with something on it.
func (c Campaign) Live() bool { return c.Running && c.Products > 0 }

// ProductsText is how many it features.
func (c Campaign) ProductsText() string { return strconv.FormatInt(c.Products, 10) }

// Href is its edit page.
func (c Campaign) Href() string { return "/admin/campaigns/" + c.Slug }

// ToggleAction is where the on/off form posts.
func (c Campaign) ToggleAction() string { return "/admin/campaigns/" + c.Slug + "/active" }

// NextActive is what the toggle would set it to.
func (c Campaign) NextActive() string {
	if c.Active {
		return "false"
	}
	return "true"
}

// ToggleLabel is what the button says.
func (c Campaign) ToggleLabel(ctx context.Context) string {
	if c.Active {
		return i18n.T(ctx, i18n.KeyAdminToggleOff)
	}
	return i18n.T(ctx, i18n.KeyAdminToggleOn)
}

// CampaignProduct is one featured product.
type CampaignProduct struct {
	Slug string
	Name string
}

// CampaignsView is the promotions page.
type CampaignsView struct {
	pages.ListBound

	Rows   []Campaign
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

// CampaignView is one campaign's edit page.
type CampaignView struct {
	Slug     string
	Title    string
	EndsAt   string
	Running  bool
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
func (v CampaignView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

// Err is why.
func (v CampaignView) Err(f string) string { return v.Errors[f] }

// ToneAction is where the tone form posts.
func (v CampaignView) ToneAction() string { return "/admin/campaigns/" + v.Slug + "/tone" }

// ImageAction is where the header upload form posts.
func (v CampaignView) ImageAction() string { return "/admin/campaigns/" + v.Slug + "/image" }

// ImageRemoveAction is where the remove form posts.
func (v CampaignView) ImageRemoveAction() string {
	return "/admin/campaigns/" + v.Slug + "/image/remove"
}

// Empty reports whether it features nothing.
func (v CampaignView) Empty() bool { return len(v.Products) == 0 }

// FeatureAction is where the add-product form posts.
func (v CampaignView) FeatureAction() string { return "/admin/campaigns/" + v.Slug + "/products" }
