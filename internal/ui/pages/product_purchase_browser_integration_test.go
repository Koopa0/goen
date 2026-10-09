//go:build integration

package pages

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
)

func TestProductChoiceAgainstAnInFlightPurchase(t *testing.T) {
	if os.Getenv("GOEN_CHROME") == "" {
		t.Skip("set GOEN_CHROME to run the product purchase browser regression")
	}
	fixtures := make(map[string]string)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for index, choice := range []string{"blue", "black"} {
			for _, outcome := range []AddOutcome{"", AddOutcomeAdded} {
				view := ProductView{
					Slug: "two-colours", Name: "Two colours", VariantID: choice,
					SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
					Available: 10, PriceCents: int64((index + 1) * 10000), AddedOutcome: outcome,
					Images: []ProductImage{{URL: "/" + choice + ".webp", Alt: choice, ShowsOption: true}},
					Options: []ProductOption{{Name: "colour", Label: "Colour", Values: []ProductOptionValue{
						{Value: "blue", Label: "Blue", Href: "/p/two-colours?colour=blue&locale=" + string(locale), Available: true, Selected: choice == "blue"},
						{Value: "black", Label: "Black", Href: "/p/two-colours?colour=black&locale=" + string(locale), Available: true, Selected: choice == "black"},
					}}},
				}
				var body strings.Builder
				body.WriteString(`<!doctype html><html><head><script src="/htmx.js" defer></script><script src="/goen.js" defer></script></head><body>`)
				count, key := "0", string(locale)+"-"+choice
				if outcome == AddOutcomeAdded {
					count, key = "1", key+"-added"
				}
				body.WriteString(`<a id="cart-link" href="/cart"><span id="cart-count">` + count + `</span></a>`)
				for _, component := range []templ.Component{productGallery(&view), productBuy(&view), productBuyBar(&view)} {
					if err := component.Render(i18n.WithLocale(t.Context(), locale), &body); err != nil {
						t.Fatal(err)
					}
				}
				body.WriteString("</body></html>")
				fixtures[key] = body.String()
			}
		}
	}
	data, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "purchases.json")
	if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	node, lookupErr := exec.LookPath("node")
	if lookupErr != nil {
		t.Fatal(lookupErr)
	}
	//nolint:gosec // G204: node is resolved on PATH and the script path and fixture are test-owned
	cmd := exec.CommandContext(t.Context(), node, "../../../scripts/product-purchase-check.mjs", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("product purchase browser regression: %v\n%s", err, output)
	}
	t.Log(string(output))
}
