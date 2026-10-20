package catalog

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
)

// 12:00 on 2 October 2026 in Taipei.
var scheduleNow = time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC)

func day(d int) time.Time { return time.Date(2026, 10, d-1, 16, 0, 0, 0, time.UTC) }

func TestNothingOfferedIsCalledLimitedTime(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for name, text := range map[string]string{
		"nav":              i18n.T(ctx, i18n.KeyDeals),
		"deals title":      DealsMeta(ctx).Title,
		"deals page":       renderComponent(t, ctx, Deals(DealsMeta(ctx), SearchView{})),
		"campaign meta":    CampaignMeta(ctx, "秋日選物", components.Photo{}).Description,
		"campaigns strip":  renderComponent(t, ctx, campaignStrip([]CampaignSummary{{Slug: "a", Title: "秋日選物", Products: 2}})),
		"campaigns header": i18n.T(ctx, i18n.KeyCampaignsRunning),
		"department notice": renderComponent(t, ctx, departmentNotice(&DepartmentNotice{
			Title: "秋日選物", Href: "/s/autumn", End: campaign.NewEnd(day(11), scheduleNow),
		})),
	} {
		if strings.Contains(text, "限時") {
			t.Errorf("%s says 限時", name)
		}
	}
	if got := DealsMeta(ctx).Title; got != "優惠" {
		t.Errorf("DealsMeta title = %q, want 優惠", got)
	}
}

func TestACampaignIsCalledACampaignInEnglish(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for key, want := range map[i18n.Key]string{
		i18n.KeyCampaignNotFound:     "Campaign not found",
		i18n.KeyCampaignNotFoundBody: "We could not find that campaign. See the deals running now.",
	} {
		if got := i18n.T(ctx, key); got != want {
			t.Errorf("i18n.T(%q) = %q, want %q", key, got, want)
		}
	}
	if got := CampaignMeta(ctx, "Autumn", components.Photo{}).Description; got != "Autumn — deals at goen" {
		t.Errorf("CampaignMeta description = %q, want %q", got, "Autumn — deals at goen")
	}
}
