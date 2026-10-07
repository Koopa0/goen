package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestTheNewProductSectionIsNotNamedLikeItsFirstField(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale  i18n.Locale
		heading string
	}{{i18n.ZhHant, "基本資料"}, {i18n.En, "Basic details"}} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			body := renderComponent(t, ctx, ProductForm(layouts.Page{Title: "Product"}, ProductView{IsNew: true}))
			if want := `<h2 class="goen-admin__heading">` + tt.heading + `</h2>`; !strings.Contains(body, want) {
				t.Errorf("new product page lacks %q", want)
			}
			if strings.Contains(body, `placeholder="ceramic-mug"`) {
				t.Error("the slug example is a placeholder that reads as a filled value")
			}
		})
	}
}

func TestATaxonomyRowSavesWhatItShows(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		want   string
	}{{i18n.ZhHant, "儲存"}, {i18n.En, "Save"}} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			view := &TaxonomyView{Brands: []Taxon{{Slug: "saelo", Name: "沙羅"}}}
			body := renderComponent(t, ctx, Taxonomy(layouts.Page{Title: "Brands"}, view))
			i := strings.Index(body, `value="rename"`)
			if i < 0 {
				t.Fatal("no row save button")
			}
			end := strings.Index(body[i:], "</button>")
			if got := body[i : i+end]; !strings.HasSuffix(strings.TrimSpace(got), tt.want) {
				t.Errorf("row button reads %q, want it to end with %q", got, tt.want)
			}
		})
	}
}
