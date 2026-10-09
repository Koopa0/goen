package assets

import (
	"encoding/hex"
	"io/fs"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// tokenHex finds a custom property declared as a six-digit hex colour, and
// tokenTranslucent one declared as rgb(r g b / alpha).
var (
	tokenHex         = regexp.MustCompile(`(?m)^\s*(--[a-z0-9-]+):\s*#([0-9a-fA-F]{6});`)
	tokenTranslucent = regexp.MustCompile(`(?m)^\s*(--[a-z0-9-]+):\s*(rgb\([^)]*\));`)
)

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

// hexTokens reads the opaque colour tokens from base.css, where both layouts
// get them, as six hex digits.
func hexTokens(t *testing.T) map[string]string {
	t.Helper()
	return baseTokens(t, tokenHex, "#")
}

// translucentTokens reads the rgb(r g b / alpha) tokens from base.css. They are
// kept apart from the hex ones because contrast reads only hex, and the NaN it
// returns for anything else passes every "less than" check.
func translucentTokens(t *testing.T) map[string]string {
	t.Helper()
	return baseTokens(t, tokenTranslucent, "")
}

// baseTokens reads the tokens decl finds in base.css. A token base.css declares
// and app.css or admin.css declares again with another value would be read by
// none of the tests below, so it fails here; a head that needs another colour
// points at a token of its own.
func baseTokens(t *testing.T, decl *regexp.Regexp, prefix string) map[string]string {
	t.Helper()

	read := func(name string) [][]string {
		sheet, err := fs.ReadFile(files, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return decl.FindAllStringSubmatch(string(sheet), -1)
	}
	tokens := make(map[string]string)
	for _, m := range read(BaseCSS) {
		if _, seen := tokens[m[1]]; !seen {
			tokens[m[1]] = m[2]
		}
	}
	for _, name := range []string{AppCSS, AdminCSS} {
		for _, m := range read(name) {
			if base, ok := tokens[m[1]]; ok && base != m[2] {
				t.Errorf("%s redeclares %s as %s%s, base.css has %s%s", name, m[1], prefix, m[2], prefix, base)
			}
		}
	}
	return tokens
}

// TestTextTokensReadOnTheGroundsTheyAreUsedOn holds the contrast of the text
// colours against the grounds a page paints. A palette is edited a value at a
// time, and a pale grey that looks fine beside its neighbours fails a reader
// outdoors; the axe gate sees only the routes it visits.
func TestTextTokensReadOnTheGroundsTheyAreUsedOn(t *testing.T) {
	t.Parallel()

	tokens := hexTokens(t)

	for _, name := range []string{"--n-0", "--n-50", "--wash", "--well", "--ink", "--muted", "--accent", "--accent-deep", "--edge", "--mark"} {
		if tokens[name] == "" {
			t.Fatalf("no stylesheet declares a hex value for %s", name)
		}
	}

	grounds := []string{"--n-0", "--n-50", "--wash", "--well"}
	for _, ink := range []string{"--ink", "--muted", "--accent", "--accent-deep"} {
		for _, ground := range grounds {
			if got := contrast(tokens[ink], tokens[ground]); got < 4.5 {
				t.Errorf("%s (#%s) on %s (#%s) = %.2f:1, want at least 4.5:1",
					ink, tokens[ink], ground, tokens[ground], got)
			}
		}
	}

	// The labels of the filled and the soft button, and text and links on a
	// blue tint: a current item, a badge, an information notice, the pay page's
	// hold; and the five badge groups.
	for _, pair := range []struct{ ink, ground string }{
		{ink: "--on-accent", ground: "--accent"},
		{ink: "--on-accent", ground: "--accent-deep"},
		{ink: "--accent-deep", ground: "--accent-faint"},
		{ink: "--accent-deep", ground: "--accent-muted"},
		{ink: "--accent", ground: "--accent-faint"},
		{ink: "--muted", ground: "--accent-faint"},
		{ink: "--error", ground: "--error-bg"},
		{ink: "--error", ground: "--error-bg-hover"},
		{ink: "--n-900", ground: "--on-ink-accent"},
		{ink: "--muted", ground: "--n-100"},
		{ink: "--warn", ground: "--warn-tint"},
		{ink: "--success", ground: "--success-tint"},
		{ink: "--error", ground: "--error-tint"},
	} {
		if tokens[pair.ink] == "" || tokens[pair.ground] == "" {
			t.Fatalf("no stylesheet declares a hex value for %s or %s", pair.ink, pair.ground)
		}
		if got := contrast(tokens[pair.ink], tokens[pair.ground]); got < 4.5 {
			t.Errorf("%s (#%s) on %s (#%s) = %.2f:1, want at least 4.5:1",
				pair.ink, tokens[pair.ink], pair.ground, tokens[pair.ground], got)
		}
	}

	// A bar is a graphical object, held to 3:1 (WCAG 1.4.11).
	if tokens["--chart-hue"] == "" {
		t.Fatalf("no stylesheet declares a hex value for --chart-hue")
	}
	for _, ground := range []string{"--n-0", "--n-50", "--n-100"} {
		if got := contrast(tokens["--chart-hue"], tokens[ground]); got < 3 {
			t.Errorf("--chart-hue (#%s) on %s (#%s) = %.2f:1, want at least 3:1",
				tokens["--chart-hue"], ground, tokens[ground], got)
		}
	}

	// A meter's unfilled part is a tint of the hue, and the filled part must
	// stand out from it.
	if tokens["--chart-hue-track"] == "" {
		t.Fatalf("no stylesheet declares a hex value for --chart-hue-track")
	}
	if got := contrast(tokens["--chart-hue"], tokens["--chart-hue-track"]); got < 3 {
		t.Errorf("--chart-hue (#%s) on --chart-hue-track (#%s) = %.2f:1, want at least 3:1",
			tokens["--chart-hue"], tokens["--chart-hue-track"], got)
	}

	// The warning mark is a triangle and a bar, and the line at 30 days is drawn
	// in --ink-2: each is held to 3:1 (WCAG 1.4.11) on the white and the grey a
	// runway row's track is drawn on.
	if tokens["--warn-mark"] == "" {
		t.Fatalf("no stylesheet declares a hex value for --warn-mark")
	}
	for _, ground := range []string{"--n-0", "--n-50", "--n-100"} {
		for _, mark := range []string{"--warn-mark", "--ink-2"} {
			if got := contrast(tokens[mark], tokens[ground]); got < 3 {
				t.Errorf("%s (#%s) on %s (#%s) = %.2f:1, want at least 3:1",
					mark, tokens[mark], ground, tokens[ground], got)
			}
		}
	}

	// The 30-day line is held by the token the rule draws it with, not by a
	// token nothing uses.
	adminSheet, err := fs.ReadFile(files, AdminCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AdminCSS, err)
	}
	line := regexp.MustCompile(`(?s)\.goen-chartrangebar__mark \{\s*fill: var\((--[a-z0-9-]+)\);`).FindStringSubmatch(string(adminSheet))
	if line == nil {
		t.Fatalf("%s has no 30-day line rule with a token colour", AdminCSS)
	}
	for _, ground := range []string{"--n-0", "--n-50", "--n-100"} {
		if got := contrast(tokens[line[1]], tokens[ground]); got < 3 {
			t.Errorf("the 30-day line %s (#%s) on %s (#%s) = %.2f:1, want at least 3:1",
				line[1], tokens[line[1]], ground, tokens[ground], got)
		}
	}

	// WCAG 1.4.11: the boundary of a control has no text to carry it.
	for _, ground := range []string{"--n-0", "--wash", "--well"} {
		if got := contrast(tokens["--edge"], tokens[ground]); got < 3 {
			t.Errorf("--edge (#%s) on %s (#%s) = %.2f:1, want at least 3:1",
				tokens["--edge"], ground, tokens[ground], got)
		}
	}
	// Nor has a period's fill, on the page or on its own track.
	track := translucentTokens(t)["--period-track"]
	if track == "" {
		t.Fatalf("no stylesheet declares --period-track as rgb(r g b / alpha)")
	}
	for _, ground := range grounds {
		checkPeriod(t, "the page", tokens["--mark"], track, ground, tokens[ground])
	}

	// The photographs are encoded on #f9f9f9; any other container ground
	// draws an edge around every product.
	if tokens["--well"] != "f9f9f9" {
		t.Errorf("--well = #%s, want #f9f9f9, the ground the photographs carry", tokens["--well"])
	}
}

// toneBlock finds a [data-tone="…"] rule and its declarations.
var toneBlock = regexp.MustCompile(`(?ms)^\[data-tone="([a-z]+)"\]\s*\{(.*?)\}`)

// toneDecl finds one declaration inside a tone block. The value is a hex colour
// or a var() naming a token.
var toneDecl = regexp.MustCompile(`(--tone-[a-z]+):\s*(#[0-9a-fA-F]{6}|var\((--[a-z0-9-]+)\));`)

// periodOverride finds the colours a head gives the period on its tone, and
// periodDecl one of them: a tone token, a token of the page's own, or a
// translucent rgb().
var (
	periodOverride = regexp.MustCompile(`(?s)\.(goen-hero__slide|goen-pagehead|goen-tiles__grid--lead)\[data-tone(?:="([a-z]+)")?\] \.ui-period \{(.*?)\}`)
	periodDecl     = regexp.MustCompile(`--period-([a-z]+):\s*(?:var\((--[a-z0-9-]+)\)|(rgb\([^)]*\)));`)
	translucent    = regexp.MustCompile(`^rgb\((\d{1,3}) (\d{1,3}) (\d{1,3}) / (0?\.\d+)\)$`)
)

// over lays a translucent rgb(r g b / alpha) on a hex ground and returns the
// colour a reader sees, blended in sRGB the way the browser paints it.
func over(t *testing.T, colour, ground string) string {
	t.Helper()
	m := translucent.FindStringSubmatch(colour)
	g, err := hex.DecodeString(ground)
	if m == nil || err != nil || len(g) != 3 {
		t.Fatalf("cannot lay %q on #%s: want rgb(r g b / alpha) on a six-digit hex", colour, ground)
	}
	alpha := alphaOf(t, colour)
	seen := make([]byte, 3)
	for i := range seen {
		c, err := strconv.Atoi(m[i+1])
		if err != nil || c > 255 {
			t.Fatalf("channel %d of %q is not 0–255", i, colour)
		}
		seen[i] = uint8(math.Round(alpha*float64(c) + (1-alpha)*float64(g[i])))
	}
	return hex.EncodeToString(seen)
}

// alphaOf is the alpha of a translucent rgb(r g b / alpha).
func alphaOf(t *testing.T, colour string) float64 {
	t.Helper()
	m := translucent.FindStringSubmatch(colour)
	if m == nil {
		t.Fatalf("%q is not rgb(r g b / alpha)", colour)
	}
	alpha, err := strconv.ParseFloat(m[4], 64)
	if err != nil {
		t.Fatalf("alpha of %q: %v", colour, err)
	}
	return alpha
}

// checkPeriod holds a period's fill to 3:1 (WCAG 1.4.11) on its ground and on
// its track laid over that ground: the edge between elapsed and to come is the
// one the track exists to show. The track against the ground is held only to
// 1.5:1, because the facts above the track state what it draws; below that a
// 4px track fades on most screens and reads as a stub.
func checkPeriod(t *testing.T, where, fill, track, groundName, ground string) {
	t.Helper()
	if got := contrast(fill, ground); math.IsNaN(got) || got < 3 {
		t.Errorf("%s: the period's fill (#%s) on %s (#%s) = %.2f:1, want at least 3:1",
			where, fill, groundName, ground, got)
	}
	seen := over(t, track, ground)
	if got := contrast(fill, seen); math.IsNaN(got) || got < 3 {
		t.Errorf("%s: the period's fill (#%s) on its track %s over %s (#%s) = %.2f:1, want at least 3:1",
			where, fill, track, groundName, seen, got)
	}
	if got := contrast(seen, ground); math.IsNaN(got) || got < 1.5 {
		t.Errorf("%s: the period's track %s over %s (#%s) is #%s, %.2f:1 against the ground, want at least 1.5:1",
			where, track, groundName, ground, seen, got)
	}
}

// toneHeads are the heads that lay the period on a tone's ground: the home
// hero, the department and campaign head, and the lead tile.
var toneHeads = []string{"goen-hero__slide", "goen-pagehead", "goen-tiles__grid--lead"}

// toneNames is pages.Tone's closed set. assets cannot import pages, whose test
// repeats the list.
var toneNames = []string{"paper", "stone", "mist", "sage", "blush", "ink"}

// TestEveryToneGroundHoldsItsText holds the text a department or campaign head
// shows to 4.5:1 on each tone's ground, and its edge and mark colours to 3:1
// (WCAG 1.4.11). A new tone is a new block, and one
// whose ground drifts from its oklch source would fail a reader without any
// route the axe gate visits showing it.
func TestEveryToneGroundHoldsItsText(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	tokens := hexTokens(t)
	rgba := translucentTokens(t)
	track := rgba["--period-track"]
	if track == "" {
		t.Fatalf("no stylesheet declares --period-track as rgb(r g b / alpha)")
	}
	// A tone's own track is its blue at no less alpha than the page's: on the
	// ink ground the 1.5:1 floor alone would pass the 22% the owner ruled too
	// faint (1.63:1).
	for name, colour := range rgba {
		if strings.HasPrefix(name, "--period-track-") && alphaOf(t, colour) < alphaOf(t, track) {
			t.Errorf("%s is %s, under the alpha of --period-track %s", name, colour, track)
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
		for _, prop := range []string{"--tone-ground", "--tone-rule", "--tone-text", "--tone-muted", "--tone-edge", "--tone-mark"} {
			if len(decl[prop]) != 6 {
				t.Fatalf("data-tone=%q declares no colour for %s", name, prop)
			}
		}
		ground := decl["--tone-ground"]
		// The mark is also the colour of a link on the tone ("see all").
		for _, prop := range []string{"--tone-text", "--tone-muted", "--tone-mark"} {
			if got := contrast(decl[prop], ground); got < 4.5 {
				t.Errorf("%s (#%s) on the %s ground (#%s) = %.2f:1, want at least 4.5:1",
					prop, decl[prop], name, ground, got)
			}
		}
		// The period sits on the tone's ground under each head. Each head
		// starts from the page's fill and track and takes only its own
		// overrides, so one head's rules cannot stand in for another's.
		for _, head := range toneHeads {
			period := map[string]string{"fill": tokens["--mark"], "track": track}
			for _, m := range periodOverride.FindAllStringSubmatch(string(sheet), -1) {
				if m[1] != head || (m[2] != "" && m[2] != name) {
					continue
				}
				for _, d := range periodDecl.FindAllStringSubmatch(m[3], -1) {
					switch {
					case d[3] != "":
						period[d[1]] = d[3]
					case strings.HasPrefix(d[2], "--tone-"):
						period[d[1]] = decl[d[2]]
					case rgba[d[2]] != "":
						period[d[1]] = rgba[d[2]]
					default:
						period[d[1]] = tokens[d[2]]
					}
				}
			}
			checkPeriod(t, head, period["fill"], period["track"], "the "+name+" ground", ground)
		}
		if got := contrast(decl["--tone-edge"], ground); got < 3 {
			t.Errorf("--tone-edge (#%s) on the %s ground (#%s) = %.2f:1, want at least 3:1",
				decl["--tone-edge"], name, ground, got)
		}
		if name == "ink" {
			continue
		}
		// A light ground is also where links and the plain text tokens land.
		for _, ink := range []string{"--muted", "--ink", "--accent"} {
			if got := contrast(tokens[ink], ground); got < 4.5 {
				t.Errorf("%s (#%s) on the %s ground (#%s) = %.2f:1, want at least 4.5:1",
					ink, tokens[ink], name, ground, got)
			}
		}
	}
}

// The review form's star picker is a control with no text, so WCAG 1.4.11 holds
// its outline and its selected fill to 3:1 against the ground the form sits on,
// and the fill must stand out from the outline around it.
func TestTheStarPickerIsVisibleOnTheReviewForm(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	tokens := hexTokens(t)
	outline := regexp.MustCompile(`(?s)\.goen-pdp__starmark svg \{\s*color: var\((--[a-z0-9-]+)\);`).FindStringSubmatch(string(sheet))
	fill := regexp.MustCompile(`(?s)\.goen-pdp__starrow:not\(:hover\)[^{]*\{\s*fill: var\((--[a-z0-9-]+)\);`).FindStringSubmatch(string(sheet))
	if outline == nil || fill == nil {
		t.Fatalf("%s has no star picker outline or selected fill rule with a token colour", AppCSS)
	}
	ground := tokens["--n-50"]
	for _, c := range []struct{ what, token, against, on string }{
		{"outline", outline[1], ground, "--n-50"},
		{"selected fill", fill[1], ground, "--n-50"},
		{"selected fill", fill[1], tokens[outline[1]], "the outline " + outline[1]},
	} {
		if got := contrast(tokens[c.token], c.against); got < 3 {
			t.Errorf("star %s %s (#%s) on %s (#%s) = %.2f:1, want at least 3:1",
				c.what, c.token, tokens[c.token], c.on, c.against, got)
		}
	}
}

func TestControlBoundariesReadOnTheirGrounds(t *testing.T) {
	t.Parallel()
	sheet, err := fs.ReadFile(files, BaseCSS)
	if err != nil {
		t.Fatal(err)
	}
	tokens := hexTokens(t)
	alias := regexp.MustCompile(`(?m)^\s*--control-boundary:\s*var\((--[a-z0-9-]+)\);`).FindStringSubmatch(string(sheet))
	if len(alias) != 2 || tokens[alias[1]] == "" {
		t.Fatal("controls need a boundary from the existing colour ramp")
	}
	tokens["--control-boundary"] = tokens[alias[1]]
	for _, ground := range []string{"--n-0", "--n-50", "--n-100"} {
		got := contrast(tokens["--control-boundary"], tokens[ground])
		if math.IsNaN(got) || got < 3 {
			t.Errorf("control boundary on %s = %.2f:1, want at least 3:1", ground, got)
		}
	}
}

// The focus ring is a graphical object (WCAG 1.4.11, 2.4.7): 3:1 on every
// ground a focusable thing sits on. The accent is dark, so the grounds that
// are dark themselves re-point --ring to white.
func TestTheFocusRingReadsOnEveryGround(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	tokens := hexTokens(t)

	for _, ground := range []string{"--n-0", "--n-50", "--wash", "--well"} {
		if got := contrast(tokens["--accent"], tokens[ground]); got < 3 {
			t.Errorf("focus ring --accent (#%s) on %s (#%s) = %.2f:1, want at least 3:1",
				tokens["--accent"], ground, tokens[ground], got)
		}
	}

	override := regexp.MustCompile(`(?s)((?:[^{}]*)\{[^{}]*--ring: var\((--[a-z0-9-]+)\);)`).FindStringSubmatch(string(sheet))
	if override == nil {
		t.Fatal("app.css does not re-point --ring on any dark ground")
	}
	selectorList, _, _ := strings.Cut(override[1], "{")
	selectors := strings.Split(strings.TrimSpace(selectorList), ",")
	for _, sel := range selectors {
		// A bare tone selector also reaches the light grounds that carry
		// the tone as data, where a white ring would be 1:1.
		if strings.HasPrefix(strings.TrimSpace(sel), "[data-tone") {
			t.Errorf("the white --ring override selects %q on any element; scope it to a dark ground", strings.TrimSpace(sel))
		}
	}
	for _, bg := range []string{tokens["--n-900"], "18181b" /* [data-tone="ink"] */} {
		if got := contrast(tokens[override[2]], bg); got < 3 {
			t.Errorf("focus ring %s (#%s) on the dark ground #%s = %.2f:1, want at least 3:1",
				override[2], tokens[override[2]], bg, got)
		}
	}
}

// A focus outline that names its own colour skips the re-pointed --ring and
// can land on a ground it does not read on. Only "none" (the ring is drawn on
// another element), "transparent" (the field draws its own border) and the
// error colour on an invalid field are allowed. A rule for
// :not(:focus-visible) is not a focus rule.
func TestEveryFocusOutlineColourIsTheRing(t *testing.T) {
	t.Parallel()

	rule := regexp.MustCompile(`([^{}]*[^(]:focus-visible[^{}]*)\{([^{}]*)\}`)
	outline := regexp.MustCompile(`outline(?:-color)?:\s*([^;]+);`)
	allowed := regexp.MustCompile(`^(?:none|transparent|var\(--error\)|2px solid var\(--ring\)|var\(--ring\))$`)
	for _, name := range []string{AppCSS, AdminCSS} {
		sheet, err := fs.ReadFile(files, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, r := range rule.FindAllStringSubmatch(string(sheet), -1) {
			for _, o := range outline.FindAllStringSubmatch(r[2], -1) {
				if !allowed.MatchString(strings.TrimSpace(o[1])) {
					t.Errorf("%s: %s draws its focus outline as %q, want var(--ring)", name, strings.TrimSpace(r[1]), strings.TrimSpace(o[1]))
				}
			}
		}
	}
}

var (
	customDecl = regexp.MustCompile(`(--[a-z0-9-]+)\s*:`)
	customUse  = regexp.MustCompile(`var\((--[a-z0-9-]+)\s*\)`)
)

// A var() naming a property no served sheet declares is invalid at
// computed-value time, and each property that reads it falls back to its
// initial value, silently.
func TestEveryCustomPropertyUsedIsDeclared(t *testing.T) {
	t.Parallel()

	declared := make(map[string]bool)
	used := make(map[string][]string)
	err := fs.WalkDir(files, "css", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(name, ".css") {
			return err
		}
		body, readErr := fs.ReadFile(files, name)
		if readErr != nil {
			return readErr
		}
		for _, m := range customDecl.FindAllStringSubmatch(string(body), -1) {
			declared[m[1]] = true
		}
		for _, m := range customUse.FindAllStringSubmatch(string(body), -1) {
			used[m[1]] = append(used[m[1]], name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for prop, sheets := range used {
		if !declared[prop] {
			t.Errorf("%s is read in %s and declared in no stylesheet", prop, sheets[0])
		}
	}
}

// The band paints its tone behind text that the page styles in --n-500, which
// no tone ground holds to 4.5:1; each of these classes must take the tone's own
// muted colour.
func TestTheBandReadsItsMutedTextFromTheTone(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	rule := regexp.MustCompile(`(?s)((?:\.goen-band [.a-z_-]+,\s*)*\.goen-band [.a-z_-]+) \{\s*color: var\(--tone-muted\);`).FindStringSubmatch(string(sheet))
	if rule == nil {
		t.Fatalf("%s has no band rule setting color: var(--tone-muted)", AppCSS)
	}
	for _, class := range []string{"goen-band__fact", "goen-tile__brand", "goen-tile__was", "goen-tile__state", "goen-tile__colours"} {
		if !strings.Contains(rule[1], ".goen-band ."+class) {
			t.Errorf("the band's muted-text rule does not name .%s", class)
		}
	}
}

// The promotion strip owns .goen-promo; a second block declaring it restyles the
// strip on every page that has one.
func TestPromoIsDeclaredOnlyForThePromotionStrip(t *testing.T) {
	t.Parallel()
	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(regexp.MustCompile(`(?m)^\.goen-promo \{`).FindAll(sheet, -1)); got != 1 {
		t.Errorf(".goen-promo is declared %d times, want once, as the strip", got)
	}
}

// The state colours are read from the rules that actually paint the boundary.
func TestInvalidAndReadOnlyFieldBoundariesHoldContrast(t *testing.T) {
	t.Parallel()
	tokens := hexTokens(t)
	base, err := fs.ReadFile(files, BaseCSS)
	if err != nil {
		t.Fatalf("read %s: %v", BaseCSS, err)
	}
	aliases := regexp.MustCompile(`(--[a-z0-9-]+):\s*var\((--[a-z0-9-]+)\);`)
	links := make(map[string]string)
	for _, m := range aliases.FindAllStringSubmatch(string(base), -1) {
		links[m[1]] = m[2]
	}
	for _, state := range []struct{ sheet, name, rule string }{
		{AppCSS, "storefront invalid", `(?s)\.goen-input--invalid,\s*\.goen-input\[aria-invalid='true'\]\s*\{[^}]*?(?:border|outline)-color:\s*var\((--[a-z0-9-]+)\)`},
		{AdminCSS, "admin invalid", `(?s)\.goen-input--invalid,\s*\.goen-input\[aria-invalid='true'\]\s*\{[^}]*?(?:border|outline)-color:\s*var\((--[a-z0-9-]+)\)`},
		{BaseCSS, "read-only hover", `(?s)\.ui-input:read-only:hover\s*\{[^}]*?border-color:\s*var\((--[a-z0-9-]+)\)`},
	} {
		sheet, readErr := fs.ReadFile(files, state.sheet)
		if readErr != nil {
			t.Fatalf("read %s: %v", state.sheet, readErr)
		}
		match := regexp.MustCompile(state.rule).FindStringSubmatch(string(sheet))
		if match == nil {
			t.Fatalf("%s has no boundary colour rule", state.name)
		}
		name := match[1]
		for i := 0; tokens[name] == "" && i < len(links); i++ {
			name = links[name]
		}
		if tokens[name] == "" {
			t.Fatalf("%s: cannot resolve %s to a hex colour", state.name, match[1])
		}
		for _, ground := range []string{"--n-0", "--n-50"} {
			got := contrast(tokens[name], tokens[ground])
			if math.IsNaN(got) || got < 3 {
				t.Errorf("%s %s (#%s) on %s (#%s) = %.2f:1, want at least 3:1", state.name, match[1], tokens[name], ground, tokens[ground], got)
			}
		}
	}
}
