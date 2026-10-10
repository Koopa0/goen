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
)

func TestCompetingProductChoicesKeepThePurchaseTogether(t *testing.T) {
	if os.Getenv("GOEN_CHROME") == "" {
		t.Skip("set GOEN_CHROME to run the product choice browser regression")
	}
	fixtures := make(map[string]string)
	for index, choice := range []string{"blue", "black", "red"} {
		view := ProductView{
			Slug: "two-colours", Name: "Two colours", VariantID: choice,
			SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
			Available: 10, PriceCents: int64((index + 1) * 10000),
			Images: []ProductImage{{URL: "/" + choice + ".webp", Alt: choice, ShowsOption: true}},
			Options: []ProductOption{{Name: "colour", Label: "Colour", Values: []ProductOptionValue{
				{Value: "blue", Label: "Blue", Href: "/p/two-colours?colour=blue", Available: true, Selected: choice == "blue"},
				{Value: "black", Label: "Black", Href: "/p/two-colours?colour=black", Available: true, Selected: choice == "black"},
				{Value: "red", Label: "Red", Href: "/p/two-colours?colour=red", Available: true, Selected: choice == "red"},
			}}},
		}
		var body strings.Builder
		body.WriteString(`<!doctype html><html><head><script src="/htmx.js" defer></script><script src="/goen.js" defer></script></head><body>`)
		for _, component := range []templ.Component{productGallery(&view), productBuy(&view), productBuyBar(&view)} {
			if err := component.Render(t.Context(), &body); err != nil {
				t.Fatal(err)
			}
		}
		body.WriteString("</body></html>")
		fixtures[choice] = body.String()
	}
	data, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "choices.json")
	if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	node, lookupErr := exec.LookPath("node")
	if lookupErr != nil {
		t.Fatal(lookupErr)
	}
	//nolint:gosec // G204: node is resolved on PATH and the script path and fixture are test-owned
	cmd := exec.CommandContext(t.Context(), node, "../../../scripts/product-choice-check.mjs", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("product choice browser regression: %v\n%s", err, output)
	}
	t.Log(string(output))
}
