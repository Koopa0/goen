//go:build integration

package product_test

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/product"
)

func TestUnknownCombinationKeepsTheCataloguePriceAndRecoverableChoices(t *testing.T) {
	store := product.NewStore(pool, slog.New(slog.DiscardHandler))
	base, err := store.Load(t.Context(), "pixelight-9-pro", nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.Load(t.Context(), "pixelight-9-pro", product.Selection{"顏色": "unknown-colour", "容量": "unknown-capacity"})
	if err != nil {
		t.Fatal(err)
	}
	if view.SelectionOK || view.CanBuy() || view.VariantID != "" {
		t.Error("an unknown combination must not name a purchasable variant")
	}
	status, markup := get(t, "pixelight-9-pro", sel("顏色", "unknown-colour", "容量", "unknown-capacity"))
	if status != 200 || !strings.Contains(markup, `class="goen-pdp__price"`) || !strings.Contains(markup, base.Price()) {
		t.Error("an unknown combination lost the catalogue price")
	}
	if !view.NeedsChoice() {
		t.Error("an unknown combination does not ask for a new choice")
	}
	for _, option := range view.Options {
		for _, value := range option.Values {
			if !value.Available || strings.Contains(value.Href, "unknown-") {
				t.Errorf("choice %q carries an impossible combination: %+v", value.Label, value)
			}
		}
	}
}

func TestUnpickedProductPhotoMatchesItsFirstColour(t *testing.T) {
	store := product.NewStore(pool, slog.New(slog.DiscardHandler))
	base, err := store.Load(t.Context(), "pixelight-9-pro", nil)
	if err != nil {
		t.Fatal(err)
	}
	var first product.Selection
	for _, option := range base.Options {
		if len(option.Values) > 1 && option.Values[0].SwatchHex != "" {
			first = product.Selection{option.Name: option.Values[0].Value}
			break
		}
	}
	if first == nil {
		t.Fatal("fixture has no colour choices")
	}
	chosen, err := store.Load(t.Context(), base.Slug, first)
	if err != nil {
		t.Fatal(err)
	}
	if len(base.Images) == 0 || len(chosen.Images) == 0 || base.Images[0].URL != chosen.Images[0].URL {
		t.Error("the unpicked product photo differs from its first colour")
	}
}
