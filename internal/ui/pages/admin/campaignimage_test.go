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
