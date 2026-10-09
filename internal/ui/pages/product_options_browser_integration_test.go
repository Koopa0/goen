//go:build integration

package pages

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestProductOptionContrastInTheBrowser(t *testing.T) {
	if os.Getenv("GOEN_CHROME") == "" {
		t.Skip("set GOEN_CHROME to run the product option contrast regression")
	}
	fixtures := make(map[string]string)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		view := ProductView{Slug: "two-colours", Name: "Two colours", SelectionOK: true, Exact: true, AnySellable: true,
			Options: []ProductOption{
				{Name: "colour", Label: "Colour", Values: []ProductOptionValue{
					{Value: "white", Label: "White", Available: true, SwatchHex: "#ffffff", Href: "/p/two-colours?colour=white"},
					{Value: "black", Label: "Black", Available: true, SwatchHex: "#18181b", Href: "/p/two-colours?colour=black"},
				}},
				capacityOption(true, "64GB/2TB"),
			}}
		var body strings.Builder
		body.WriteString(`<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/base.css"><link rel="stylesheet" href="/app.css"></head><body>`)
		if err := productBuy(&view).Render(i18n.WithLocale(t.Context(), locale), &body); err != nil {
			t.Fatal(err)
		}
		body.WriteString("</body></html>")
		fixtures[string(locale)] = body.String()
	}
	data, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "options.json")
	if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	node, lookupErr := exec.LookPath("node")
	if lookupErr != nil {
		t.Fatal(lookupErr)
	}
	//nolint:gosec // G204: node is resolved on PATH; the script and fixture are test-owned.
	cmd := exec.CommandContext(t.Context(), node, "../../../scripts/product-options-check.mjs", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("product option browser regression: %v\n%s", err, output)
	}
	t.Log(string(output))
}
