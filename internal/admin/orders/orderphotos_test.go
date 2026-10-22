package orders

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
)

// TestOrderLinePhoto: an admin order line shows its photo or the empty well.
func TestOrderLinePhoto(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, present := range []bool{true, false} {
			t.Run(locale.Tag()+"/photo="+map[bool]string{true: "present", false: "missing"}[present], func(t *testing.T) {
				t.Parallel()
				line := Line{Name: "Recorded line name", Label: "Blue", UnitCents: 10000, Quantity: 2}
				alt := "藍色商品的正面"
				if locale == i18n.En {
					alt = "The blue product, front view"
				}
				if present {
					line.ImageURL = assets.Image{BundledKey: "pixelight-9-pro-02.webp"}.URL()
					line.ImageSrcset = assets.Image{BundledKey: "pixelight-9-pro-02.webp"}.Srcset()
					line.ImageAlt = alt
				}
				doc, err := html.Parse(strings.NewReader(renderOrder(t, locale, &OrderView{Number: "GO-1", Lines: []Line{line}})))
				if err != nil {
					t.Fatal(err)
				}
				var photos []*html.Node
				wells := 0
				for n := range doc.Descendants() {
					attrs := make(map[string]string)
					for _, a := range n.Attr {
						attrs[a.Key] = a.Val
					}
					if n.Data == "img" && attrs["class"] == "goen-order__image" {
						photos = append(photos, n)
					}
					if n.Data == "span" && attrs["class"] == "goen-line__ph" && attrs["aria-hidden"] == "true" {
						wells++
					}
				}
				if !present {
					if len(photos) != 0 || wells != 1 {
						t.Errorf("missing photo renders %d images and %d empty wells, want zero and one", len(photos), wells)
					}
					return
				}
				if len(photos) != 1 || wells != 0 {
					t.Fatalf("present photo renders %d images and %d empty wells, want one and zero", len(photos), wells)
				}
				got := make(map[string]string)
				for _, a := range photos[0].Attr {
					got[a.Key] = a.Val
				}
				want := map[string]string{"class": "goen-order__image", "src": line.ImageURL, "srcset": line.ImageSrcset, "sizes": "40px", "alt": alt, "width": "40", "height": "40", "loading": "lazy", "decoding": "async"}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("line photo attributes (-want +got):\n%s", diff)
				}
				for parent := photos[0].Parent; parent != nil; parent = parent.Parent {
					if parent.Data == "a" {
						t.Error("order photo links to the current product page")
					}
				}
			})
		}
	}
}

// TestOrderPickingSlipNoPhoto: picking slip lines render no photo block.
func TestOrderPickingSlipNoPhoto(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			slip := &OrderView{OutstandingQuantities: true, Number: "GO-1", Lines: []Line{{SKU: "SKU-A", Name: "Slip line", Quantity: 2, UnitCents: 100}}}
			out := renderOrder(t, locale, slip)
			for _, banned := range []string{"goen-order__photo", "goen-line__ph", "goen-order__image"} {
				if strings.Contains(out, banned) {
					t.Errorf("picking slip line renders %q, want no photo block", banned)
				}
			}
			if !strings.Contains(out, "Slip line") {
				t.Error("picking slip line name missing from render")
			}
		})
	}
}
