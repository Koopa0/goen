package assets

import (
	"encoding/hex"
	"io/fs"
	"math"
	"regexp"
	"strings"
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

func luminance(h string) float64 {
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 3 {
		return math.NaN()
	}
	return 0.2126*linear(b[0]) + 0.7152*linear(b[1]) + 0.0722*linear(b[2])
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

// toneBlock finds a [data-tone="…"] rule and its declarations.
var toneBlock = regexp.MustCompile(`(?s)\[data-tone="([a-z]+)"\]\s*\{(.*?)\}`)

// toneDecl finds one declaration inside a tone block. The value is a hex colour
// or a var() naming a token.
var toneDecl = regexp.MustCompile(`(--tone-[a-z]+):\s*(#[0-9a-fA-F]{6}|var\((--[a-z0-9-]+)\));`)

// toneNames is pages.Tone's closed set. assets cannot import pages, whose test
// repeats the list.
var toneNames = []string{"paper", "stone", "mist", "sage", "blush", "ink"}

// TestEveryToneGroundHoldsItsText holds the text a department or campaign head
// shows to 4.5:1 on each tone's ground. A new tone is a new block, and one
// whose ground drifts from its oklch source would fail a reader without any
// route the axe gate visits showing it.
func TestEveryToneGroundHoldsItsText(t *testing.T) {
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

	blocks := make(map[string]map[string]string)
	for _, m := range toneBlock.FindAllStringSubmatch(string(sheet), -1) {
		if _, dup := blocks[m[1]]; dup {
			t.Errorf("%s declares data-tone=%q twice", AppCSS, m[1])
		}
		decls := make(map[string]string)
		for _, d := range toneDecl.FindAllStringSubmatch(m[2], -1) {
			if d[3] != "" {
				decls[d[1]] = tokens[d[3]]
			} else {
				decls[d[1]] = strings.TrimPrefix(d[2], "#")
			}
		}
		blocks[m[1]] = decls
	}
	if len(blocks) != len(toneNames) {
		t.Errorf("%s has %d data-tone blocks, want the %d of the closed set", AppCSS, len(blocks), len(toneNames))
	}

	for _, name := range toneNames {
		decl := blocks[name]
		for _, prop := range []string{"--tone-ground", "--tone-rule", "--tone-text", "--tone-muted"} {
			if len(decl[prop]) != 6 {
				t.Fatalf("data-tone=%q declares no colour for %s", name, prop)
			}
		}
		ground := decl["--tone-ground"]
		for _, prop := range []string{"--tone-text", "--tone-muted"} {
			if got := contrast(decl[prop], ground); got < 4.5 {
				t.Errorf("%s (#%s) on the %s ground (#%s) = %.2f:1, want at least 4.5:1",
					prop, decl[prop], name, ground, got)
			}
		}
		if name == "ink" {
			continue
		}
		// A light ground is also where links and the plain text tokens land.
		for _, ink := range []string{"--n-500", "--n-900", "--accent-text"} {
			if got := contrast(tokens[ink], ground); got < 4.5 {
				t.Errorf("%s (#%s) on the %s ground (#%s) = %.2f:1, want at least 4.5:1",
					ink, tokens[ink], name, ground, got)
			}
		}
	}
}

// A visitor who asked for less motion must get no autoplay: the progress fill
// is what advances the carousel, so switching its animation off and hiding the
// pause control is what keeps the slides still.
func TestTheCarouselDoesNotAdvanceUnderReducedMotion(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	css := string(sheet)
	start := strings.Index(css, "@media (prefers-reduced-motion: reduce) {\n  .goen-hero.is-playing")
	if start < 0 {
		t.Fatal("app.css has no reduced-motion block for the carousel's autoplay")
	}
	block := css[start : start+strings.Index(css[start:], "\n}\n\n")+3]
	for _, want := range []string{
		`.goen-hero.is-playing .goen-hero__dot[aria-current="true"]::after`,
		"animation: none;",
		".goen-hero__pause {\n    display: none;",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the reduced-motion block does not contain %q:\n%s", want, block)
		}
	}
}
