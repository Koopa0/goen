package layouts_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// A shopper's page must not carry the back office's rules, and the back
// office's page must.
func TestOnlyTheBackOfficeLinksItsStylesheet(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	var shop, admin strings.Builder
	if err := layouts.Base(layouts.Page{}).Render(ctx, &shop); err != nil {
		t.Fatal(err)
	}
	if err := layouts.Admin(layouts.Page{}, "").Render(ctx, &admin); err != nil {
		t.Fatal(err)
	}
	link := `href="` + assets.URL(assets.AdminCSS) + `"`
	if strings.Contains(shop.String(), "css/app/admin.css") {
		t.Error("the storefront shell links the back office stylesheet")
	}
	if !strings.Contains(admin.String(), link) {
		t.Errorf("the admin shell does not link %s", link)
	}
	if !strings.Contains(admin.String(), assets.URL(assets.AppCSS)) {
		t.Error("the admin shell dropped app.css, which its pages still build on")
	}
}
