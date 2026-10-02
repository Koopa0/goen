package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

const seedBanner = "campaign-banner-01.webp"

func TestCampaignImageFormFlagsAWrongFieldForAssistiveTech(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	page := renderComponent(t, ctx, CampaignForm(layouts.Page{Title: "c"}, CampaignView{
		Slug:   "c",
		Errors: map[string]string{"alt": "alt needed", "image": "not an image"},
		Image:  Header{Key: seedBanner, Alt: "Products on sale", Width: 1600},
	}))
	for _, want := range []string{
		`aria-describedby="c-alt-error"`, `aria-describedby="c-image-error"`, `alt needed`, `not an image`,
		`action="/admin/campaigns/c/image"`, `action="/admin/campaigns/c/image/remove"`, `enctype="multipart/form-data"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the campaign image form lacks %q", want)
		}
	}
	if strings.Count(page, `aria-invalid="true"`) != 2 {
		t.Error("each refused field should be aria-invalid")
	}
}

func TestTheCampaignPageIsTitledAndEditsItsDatesAndAddsProductsBySearch(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	page := renderComponent(t, ctx, CampaignForm(layouts.Page{Title: "c"}, CampaignView{
		Slug: "autumn-picks",
		CampaignDetail: CampaignDetail{
			Title: "秋季精選", StartsAtInput: "2026-10-01T09:00", EndsAtInput: "2026-10-31T23:59", Active: true,
		},
		Term:    "mug",
		Matches: []CampaignProduct{{Slug: "ceramic-mug", Name: "陶瓷馬克杯"}},
	}))
	_, afterH1, _ := strings.Cut(page, `<h1 class="ui-page-head__title">`)
	if h1, _, _ := strings.Cut(afterH1, "</h1>"); h1 != "秋季精選" {
		t.Errorf("the heading is %q, want the campaign's title", h1)
	}
	for _, want := range []string{
		`action="/admin/campaigns/autumn-picks/window"`,
		`name="starts_at" type="datetime-local" required value="2026-10-01T09:00"`,
		`name="ends_at" type="datetime-local" required value="2026-10-31T23:59"`,
		`action="/admin/campaigns/autumn-picks/active"`, `name="back" value="detail"`, `name="active" value="false"`,
		`for="c-starts"`, `for="c-ends"`,
		`method="get" action="/admin/campaigns/autumn-picks"`, `name="find"`, `for="k-find"`,
		`陶瓷馬克杯`, `name="product" value="ceramic-mug"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the campaign page lacks %q", want)
		}
	}
	if strings.Contains(page, `id="k-product"`) {
		t.Error("adding a product still asks for a typed slug")
	}
}
