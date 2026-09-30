package layouts

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

func TestHeadNamesAnAbsoluteShareImageOnlyWhereTheOriginIsKnown(t *testing.T) {
	t.Parallel()
	origin, _, ok := web.SiteOrigin("https://shop.example/")
	if !ok {
		t.Fatal("origin")
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(WithSiteOrigin(t.Context(), origin), locale)
		var b strings.Builder
		if err := Base(Page{Title: "t"}).Render(ctx, &b); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			`<meta property="og:image" content="https://shop.example` + assets.URL(assets.OGDefaultImage) + `"`,
			`<meta property="og:image:width" content="1200"`,
			`<meta property="og:image:height" content="630"`,
			`<meta property="og:image:alt" content="` + i18n.T(ctx, i18n.KeyOGImageAlt) + `"`,
		} {
			if !strings.Contains(b.String(), want) {
				t.Errorf("%s head omits %s", locale, want)
			}
		}
	}
	var bare strings.Builder
	if err := Base(Page{Title: "t"}).Render(i18n.WithLocale(t.Context(), i18n.En), &bare); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bare.String(), "og:image") {
		t.Error("a head with no configured origin sent a relative share image")
	}
}
