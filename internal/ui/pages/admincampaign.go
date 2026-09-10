package pages

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// AdminCampaign is one promotion as the back office sees it.
type AdminCampaign struct {
	Slug     string
	Title    string
	Products int64
	Active   bool
	Running  bool
	StartsAt string
	EndsAt   string
}

// State is the word a staff member scans for; active and running differ.
func (c AdminCampaign) State(ctx context.Context) string {
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
func (c AdminCampaign) Live() bool { return c.Running && c.Products > 0 }

// ProductsText is how many it features.
func (c AdminCampaign) ProductsText() string { return strconv.FormatInt(c.Products, 10) }

// Href is its edit page.
func (c AdminCampaign) Href() string { return "/admin/campaigns/" + c.Slug }

// ToggleAction is where the on/off form posts.
func (c AdminCampaign) ToggleAction() string { return "/admin/campaigns/" + c.Slug + "/active" }

// NextActive is what the toggle would set it to.
func (c AdminCampaign) NextActive() string {
	if c.Active {
		return "false"
	}
	return "true"
}

// ToggleLabel is what the button says.
func (c AdminCampaign) ToggleLabel(ctx context.Context) string {
	if c.Active {
		return i18n.T(ctx, i18n.KeyAdminToggleOff)
	}
	return i18n.T(ctx, i18n.KeyAdminToggleOn)
}

// AdminCampaignProduct is one featured product.
type AdminCampaignProduct struct {
	Slug string
	Name string
}

// AdminCampaignsView is the promotions page.
type AdminCampaignsView struct {
	Rows   []AdminCampaign
	Notice string
	Errors map[string]string
	Draft  AdminCampaignDraft
}

// AdminCampaignDraft carries a refused form's values back.
type AdminCampaignDraft struct {
	Slug  string
	Title string
	Days  string
}

// Empty reports whether nothing has been created.
func (v AdminCampaignsView) Empty() bool { return len(v.Rows) == 0 }

// HasErr reports whether a field was refused.
func (v AdminCampaignsView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

// Err is why.
func (v AdminCampaignsView) Err(f string) string { return v.Errors[f] }

// AdminCampaignView is one campaign's edit page.
type AdminCampaignView struct {
	Slug     string
	Title    string
	EndsAt   string
	Running  bool
	Products []AdminCampaignProduct
	Notice   string
}

// Empty reports whether it features nothing.
func (v AdminCampaignView) Empty() bool { return len(v.Products) == 0 }

// FeatureAction is where the add-product form posts.
func (v AdminCampaignView) FeatureAction() string { return "/admin/campaigns/" + v.Slug + "/products" }
