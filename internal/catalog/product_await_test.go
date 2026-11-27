package catalog

import (
	"strings"
	"testing"
)

// TestProductAwaitsBuyBox holds that the product page's first paint waits for
// its buy column. The column comes after the gallery, so the first frame, and
// the snapshot a page transition takes of it, holds the photo and the price
// together rather than half a page.
func TestProductAwaitsBuyBox(t *testing.T) {
	t.Parallel()
	v := &ProductView{
		Name: "Pixelight 9 Pro", Slug: "pixelight-9-pro",
		SelectionOK: true, Sellable: true, AnySellable: true, PriceCents: 100,
	}
	html := renderToString(t, Product(ProductMeta(v), v))
	if !strings.Contains(html, `<link rel="expect" href="#buybox" blocking="render">`) {
		t.Error(`the product page does not hold its first paint for #buybox`)
	}
	gallery, buy := strings.Index(html, `id="gallery"`), strings.Index(html, `id="buybox"`)
	if gallery < 0 || buy < 0 || buy < gallery {
		t.Errorf("#gallery at %d and #buybox at %d: the awaited column must follow the gallery", gallery, buy)
	}
}
