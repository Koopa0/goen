package assets

import (
	"io/fs"
	"math"
	"regexp"
	"strconv"
	"testing"
)

// tokenHex finds a custom property declared as a six-digit hex colour.
var tokenHex = regexp.MustCompile(`(?m)^\s*(--[a-z0-9-]+):\s*#([0-9a-fA-F]{6});`)

// linear is one sRGB channel as relative luminance counts it.
func linear(c uint8) float64 {
	v := float64(c) / 255
	if v <= 0.03928 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func luminance(hex string) float64 {
	n, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return math.NaN()
	}
	return 0.2126*linear(uint8(n>>16)) + 0.7152*linear(uint8(n>>8)) + 0.0722*linear(uint8(n))
}

func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestTextTokensReadOnTheGroundsTheyAreUsedOn holds the contrast of the text
// colours against the grounds a page paints. A palette is edited a value at a
// time, and a pale grey that looks fine beside its neighbours fails a reader
// outdoors; the axe gate sees only the routes it visits.
func TestTextTokensReadOnTheGroundsTheyAreUsedOn(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	tokens := make(map[string]string)
	for _, m := range tokenHex.FindAllStringSubmatch(string(sheet), -1) {
		if _, seen := tokens[m[1]]; !seen {
			tokens[m[1]] = m[2]
		}
	}

	for _, name := range []string{"--n-0", "--n-50", "--n-500", "--n-900", "--accent-text", "--photo"} {
		if tokens[name] == "" {
			t.Fatalf("%s declares no hex value for %s", AppCSS, name)
		}
	}

	for _, ink := range []string{"--n-500", "--n-900", "--accent-text"} {
		for _, ground := range []string{"--n-0", "--n-50"} {
			if got := contrast(tokens[ink], tokens[ground]); got < 4.5 {
				t.Errorf("%s (#%s) on %s (#%s) = %.2f:1, want at least 4.5:1",
					ink, tokens[ink], ground, tokens[ground], got)
			}
		}
	}

	// The photographs are encoded on #f9f9f9; any other container ground
	// draws an edge around every product.
	if tokens["--photo"] != "f9f9f9" {
		t.Errorf("--photo = #%s, want #f9f9f9, the ground the photographs carry", tokens["--photo"])
	}
}
