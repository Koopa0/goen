package assets

import (
	"encoding/hex"
	"io/fs"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/ui/tone"
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

// contrast is the WCAG ratio of two six-digit hex colours, and 0 when either is
// not one: NaN passes every "less than" check, so a colour a test could not
// read would pass as readable.
func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if math.IsNaN(la) || math.IsNaN(lb) {
		return 0
	}
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
// kept apart from the hex ones because contrast reads only hex, and counts
// anything else as 0:1, which fails every check.
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

// aliasTokens reads the tokens base.css declares as var(--other), by name.
func aliasTokens(t *testing.T) map[string]string {
	t.Helper()
	base, err := fs.ReadFile(files, BaseCSS)
	if err != nil {
		t.Fatalf("read %s: %v", BaseCSS, err)
	}
	links := make(map[string]string)
	for _, m := range regexp.MustCompile(`(--[a-z0-9-]+):\s*var\((--[a-z0-9-]+)\);`).FindAllStringSubmatch(string(base), -1) {
		links[m[1]] = m[2]
	}
	return links
}

// resolve follows name through the aliases to the hex colour it ends at, or
// returns "" when it ends nowhere.
func resolve(name string, tokens, links map[string]string) string {
	for i := 0; tokens[name] == "" && i < len(links); i++ {
		name = links[name]
	}
	return tokens[name]
}

// roleDecl finds a custom property declared as light-dark(var(--day),
// var(--night)), and roleName any declaration of a system role.
var (
	roleDecl = regexp.MustCompile(`(?m)^\s*(--[a-z0-9-]+):\s*light-dark\(\s*var\((--[a-z0-9-]+)\)\s*,\s*var\((--[a-z0-9-]+)\)\s*\);`)
	roleName = regexp.MustCompile(`(?m)^\s*(--sys-[a-z0-9-]+)\s*:`)
)

// role is a system role's two colours as six hex digits, and the tokens its
// light-dark() names for them.
type role struct {
	day, night           string
	dayToken, nightToken string
}

// roleTokens reads the --sys-* roles from base.css, each resolved through the
// aliases to a day and a night hex colour. A role that resolves on one side
// only would make every contrast it enters NaN, which passes a "less than"
// check, so it stops the test; so does a role a surface sheet declares again,
// which none of the checks would read.
func roleTokens(t *testing.T) map[string]role {
	t.Helper()
	base, err := fs.ReadFile(files, BaseCSS)
	if err != nil {
		t.Fatalf("read %s: %v", BaseCSS, err)
	}
	tokens, links := hexTokens(t), aliasTokens(t)
	roles := make(map[string]role)
	for _, m := range roleDecl.FindAllStringSubmatch(string(base), -1) {
		if !strings.HasPrefix(m[1], "--sys-") {
			continue
		}
		if _, dup := roles[m[1]]; dup {
			t.Fatalf("%s declares %s with light-dark() twice", BaseCSS, m[1])
		}
		r := role{day: resolve(m[2], tokens, links), night: resolve(m[3], tokens, links), dayToken: m[2], nightToken: m[3]}
		if r.day == "" || r.night == "" {
			t.Fatalf("%s: %s is light-dark(var(%s), var(%s)), which resolves to day %q and night %q; want a hex colour on both sides",
				BaseCSS, m[1], m[2], m[3], r.day, r.night)
		}
		roles[m[1]] = r
	}
	for _, m := range roleName.FindAllStringSubmatch(string(base), -1) {
		if _, ok := roles[m[1]]; !ok {
			t.Fatalf("%s declares %s, but not as light-dark(var(--day), var(--night))", BaseCSS, m[1])
		}
	}
	for _, name := range []string{AppCSS, AdminCSS} {
		sheet, err := fs.ReadFile(files, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range roleName.FindAllStringSubmatch(string(sheet), -1) {
			t.Errorf("%s declares %s again; a role is declared once, in %s", name, m[1], BaseCSS)
		}
	}
	return roles
}

// lightDarkTokens reads every custom property base.css declares as
// light-dark(var(--day), var(--night)), the roles and the tokens of the ground
// alike, each resolved through the aliases to a day and a night hex colour.
func lightDarkTokens(t *testing.T) map[string]role {
	t.Helper()
	base, err := fs.ReadFile(files, BaseCSS)
	if err != nil {
		t.Fatalf("read %s: %v", BaseCSS, err)
	}
	tokens, links := hexTokens(t), aliasTokens(t)
	pairs := make(map[string]role)
	for _, m := range roleDecl.FindAllStringSubmatch(string(base), -1) {
		if _, dup := pairs[m[1]]; dup {
			t.Fatalf("%s declares %s with light-dark() twice", BaseCSS, m[1])
		}
		r := role{day: resolve(m[2], tokens, links), night: resolve(m[3], tokens, links), dayToken: m[2], nightToken: m[3]}
		if r.day == "" || r.night == "" {
			t.Fatalf("%s: %s is light-dark(var(%s), var(%s)), which resolves to day %q and night %q; want a hex colour on both sides",
				BaseCSS, m[1], m[2], m[3], r.day, r.night)
		}
		pairs[m[1]] = r
	}
	return pairs
}

// TestBaseCSSRoleContrast checks that each system role reads on the grounds it
// is drawn on, by day and by night. Only the day is on screen while the
// storefront's color-scheme is light, so no page the axe gate visits shows the
// night; this is the only check it gets.
func TestBaseCSSRoleContrast(t *testing.T) {
	t.Parallel()

	roles := roleTokens(t)
	layers := stateLayers(t)
	grounds := []string{
		"--sys-surface", "--sys-surface-container-lowest", "--sys-surface-container-low",
		"--sys-surface-container", "--sys-surface-container-high", "--sys-surface-container-highest",
	}
	inks := []string{
		"--sys-on-surface", "--sys-on-surface-variant", "--sys-primary",
		"--sys-secondary", "--sys-tertiary", "--sys-error",
	}
	// Text on the role it is named to sit on, and a link inside a chosen option.
	pairs := []struct{ ink, ground string }{
		{ink: "--sys-on-primary", ground: "--sys-primary"},
		{ink: "--sys-on-primary-container", ground: "--sys-primary-container"},
		{ink: "--sys-primary", ground: "--sys-primary-container"},
		{ink: "--sys-on-secondary", ground: "--sys-secondary"},
		{ink: "--sys-on-secondary-container", ground: "--sys-secondary-container"},
		{ink: "--sys-on-tertiary", ground: "--sys-tertiary"},
		{ink: "--sys-on-tertiary-container", ground: "--sys-tertiary-container"},
		{ink: "--sys-on-error", ground: "--sys-error"},
		{ink: "--sys-on-error-container", ground: "--sys-error-container"},
		{ink: "--sys-inverse-on-surface", ground: "--sys-inverse-surface"},
		{ink: "--sys-inverse-primary", ground: "--sys-inverse-surface"},
		{ink: "--sys-on-action", ground: "--sys-action"},
	}
	for _, name := range slices.Concat(grounds, inks, []string{"--sys-outline", "--sys-action"}) {
		if _, ok := roles[name]; !ok {
			t.Fatalf("%s declares no role %s", BaseCSS, name)
		}
	}
	for _, p := range pairs {
		if _, ok := roles[p.ink]; !ok {
			t.Fatalf("%s declares no role %s", BaseCSS, p.ink)
		}
		if _, ok := roles[p.ground]; !ok {
			t.Fatalf("%s declares no role %s", BaseCSS, p.ground)
		}
	}

	for _, scheme := range []struct {
		name   string
		colour func(role) string
		token  func(role) string
	}{
		{"day", func(r role) string { return r.day }, func(r role) string { return r.dayToken }},
		{"night", func(r role) string { return r.night }, func(r role) string { return r.nightToken }},
	} {
		check := func(ink, ground string, want float64) {
			i, g := roles[ink], roles[ground]
			if got := contrast(scheme.colour(i), scheme.colour(g)); math.IsNaN(got) || got < want {
				t.Errorf("%s: %s (%s #%s) on %s (%s #%s) = %.2f:1, want at least %g:1",
					scheme.name, ink, scheme.token(i), scheme.colour(i), ground, scheme.token(g), scheme.colour(g), got, want)
			}
		}
		for _, ground := range grounds {
			for _, ink := range inks {
				check(ink, ground, 4.5)
			}
			// WCAG 1.4.11: the boundary of a control has no text to carry it,
			// and a filled control's boundary is its fill.
			check("--sys-outline", ground, 3)
			check("--sys-action", ground, 3)
		}
		for _, p := range pairs {
			check(p.ink, p.ground, 4.5)
		}
		// A hovered or pressed filled button lays its label's colour over its
		// fill, and the label has to read on what that leaves.
		label, fill := roles["--sys-on-action"], roles["--sys-action"]
		for _, state := range []string{"--state-hover", "--state-press"} {
			ground := mix(scheme.colour(label), scheme.colour(fill), layers[state])
			if got := contrast(scheme.colour(label), ground); got < 4.5 {
				t.Errorf("%s: --sys-on-action (#%s) on --sys-action under %s (%g%% of the label, #%s) = %.2f:1, want at least 4.5:1",
					scheme.name, scheme.colour(label), state, layers[state]*100, ground, got)
			}
		}
	}
}

// stateLayer finds a state layer token: how much of a control's text colour
// is laid over its ground, as a percentage.
var stateLayer = regexp.MustCompile(`(?m)^\s*(--state-[a-z]+):\s*(\d+(?:\.\d+)?)%;`)

// stateLayers reads the state layers from base.css as fractions, and stops the
// test when hover or press is missing.
func stateLayers(t *testing.T) map[string]float64 {
	t.Helper()
	base, err := fs.ReadFile(files, BaseCSS)
	if err != nil {
		t.Fatalf("read %s: %v", BaseCSS, err)
	}
	layers := make(map[string]float64)
	for _, m := range stateLayer.FindAllStringSubmatch(string(base), -1) {
		v, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			t.Fatalf("%s: %s = %s%%: %v", BaseCSS, m[1], m[2], err)
		}
		layers[m[1]] = v / 100
	}
	for _, name := range []string{"--state-hover", "--state-press"} {
		if layers[name] == 0 {
			t.Fatalf("%s declares no %s as a percentage", BaseCSS, name)
		}
	}
	return layers
}

// mix is color-mix(in srgb, top p, ground) of two six-digit hex colours, as six
// hex digits, or "" when either is not one.
func mix(top, ground string, p float64) string {
	a, errA := hex.DecodeString(top)
	b, errB := hex.DecodeString(ground)
	if errA != nil || errB != nil || len(a) != 3 || len(b) != 3 {
		return ""
	}
	out := make([]byte, 3)
	for i := range out {
		out[i] = uint8(math.Round(p*float64(a[i]) + (1-p)*float64(b[i])))
	}
	return hex.EncodeToString(out)
}

// roleFallback finds the rule a browser without light-dark() reads instead.
var roleFallback = regexp.MustCompile(`(?s)@supports not \(color: light-dark\(#000, #fff\)\) \{\s*:root \{(.*?)\}\s*\}`)

// TestBaseCSSRoleFallback checks that a browser without light-dark() gets the
// day colour of each role and of each token of the ground drawn with
// light-dark(), such as --price. One missing from the fallback is unset there,
// and one that names another token draws a colour no scheme has.
func TestBaseCSSRoleFallback(t *testing.T) {
	t.Parallel()

	base, err := fs.ReadFile(files, BaseCSS)
	if err != nil {
		t.Fatalf("read %s: %v", BaseCSS, err)
	}
	block := roleFallback.FindSubmatch(base)
	if block == nil {
		t.Fatalf("%s has no @supports not (color: light-dark(#000, #fff)) rule for :root", BaseCSS)
	}
	fallback := make(map[string]string)
	for _, m := range regexp.MustCompile(`(--[a-z0-9-]+):\s*var\((--[a-z0-9-]+)\);`).FindAllStringSubmatch(string(block[1]), -1) {
		fallback[m[1]] = m[2]
	}
	roleTokens(t)
	roles := lightDarkTokens(t)
	for name, r := range roles {
		got, set := fallback[name]
		switch {
		case !set:
			t.Errorf("without light-dark(), %s is unset, want var(%s), its day colour", name, r.dayToken)
		case got != r.dayToken:
			t.Errorf("without light-dark(), %s = var(%s), want var(%s), its day colour", name, got, r.dayToken)
		}
	}
	for name := range fallback {
		if _, ok := roles[name]; !ok {
			t.Errorf("the light-dark() fallback sets %s, which no light-dark() role declares", name)
		}
	}
}

// TestBaseCSSTokenContrast checks that the text tokens read on the grounds they
// are used on. It holds the contrast of the text colours against the grounds a
// page paints. A palette is edited a value at a time, and a pale grey that looks
// fine beside its neighbours fails a reader outdoors; the axe gate sees only the
// routes it visits.
func TestBaseCSSTokenContrast(t *testing.T) {
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
	// hold; and the badge groups.
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
		{ink: "--n-0", ground: "--n-900"},
	} {
		if tokens[pair.ink] == "" || tokens[pair.ground] == "" {
			t.Fatalf("no stylesheet declares a hex value for %s or %s", pair.ink, pair.ground)
		}
		if got := contrast(tokens[pair.ink], tokens[pair.ground]); got < 4.5 {
			t.Errorf("%s (#%s) on %s (#%s) = %.2f:1, want at least 4.5:1",
				pair.ink, tokens[pair.ink], pair.ground, tokens[pair.ground], got)
		}
	}

	// A bar, and the line of the period it is compared with, are graphical
	// objects, held to 3:1 (WCAG 1.4.11).
	for _, ink := range []string{"--chart-hue", "--chart-previous"} {
		if tokens[ink] == "" {
			t.Fatalf("no stylesheet declares a hex value for %s", ink)
		}
		for _, ground := range []string{"--n-0", "--n-50", "--n-100"} {
			if got := contrast(tokens[ink], tokens[ground]); got < 3 {
				t.Errorf("%s (#%s) on %s (#%s) = %.2f:1, want at least 3:1",
					ink, tokens[ink], ground, tokens[ground], got)
			}
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

	// A filled button is a block of colour like a photograph's frame, --well;
	// under 2:1 against it, the night's light button reads as one more frame.
	for _, name := range []string{"--day-action", "--night-action"} {
		if tokens[name] == "" {
			t.Fatalf("no stylesheet declares a hex value for %s", name)
		}
		if got := contrast(tokens[name], tokens["--well"]); got < 2 {
			t.Errorf("%s (#%s) on --well (#%s) = %.2f:1, want at least 2:1",
				name, tokens[name], tokens["--well"], got)
		}
	}

	// The photographs are encoded on #f9f9f9; any other container ground
	// draws an edge around every product.
	if tokens["--well"] != "f9f9f9" {
		t.Errorf("--well = #%s, want #f9f9f9, the ground the photographs carry", tokens["--well"])
	}
}

// toneBlock finds a [data-tone="…"] rule and its declarations.
var toneBlock = regexp.MustCompile(`(?ms)^\[data-tone="([a-z]+)"\]\s*\{(.*?)\}`)

// toneValue is a colour a tone block may name: a hex colour or a var() naming
// a token. toneDecl finds one declaration inside a tone block, either such a
// value or light-dark() of two of them.
const toneValue = `#[0-9a-fA-F]{6}|var\(--[a-z0-9-]+\)`

var toneDecl = regexp.MustCompile(`(--tone-[a-z]+):\s*(light-dark\(\s*(` + toneValue + `)\s*,\s*(` + toneValue + `)\s*\)|` + toneValue + `);`)

// toneColour is one tone declaration: its day and night colours as six hex
// digits, the day value as written, and whether light-dark() gives it a night
// of its own. A value without light-dark() is drawn in both schemes.
type toneColour struct {
	day, night string
	dayValue   string
	lightDark  bool
}

// toneBlocks reads each tone's declarations from the storefront sheet, a var()
// resolved through tokens and links.
func toneBlocks(t *testing.T, sheet string, tokens, links map[string]string) map[string]map[string]toneColour {
	t.Helper()
	hexOf := func(v string) string {
		if name, ok := strings.CutPrefix(v, "var("); ok {
			return resolve(strings.TrimSuffix(name, ")"), tokens, links)
		}
		return strings.TrimPrefix(v, "#")
	}
	blocks := make(map[string]map[string]toneColour)
	for _, m := range toneBlock.FindAllStringSubmatch(sheet, -1) {
		if _, dup := blocks[m[1]]; dup {
			t.Errorf("%s declares data-tone=%q twice", AppCSS, m[1])
		}
		decls := make(map[string]toneColour)
		for _, d := range toneDecl.FindAllStringSubmatch(m[2], -1) {
			if d[3] != "" {
				decls[d[1]] = toneColour{day: hexOf(d[3]), night: hexOf(d[4]), dayValue: d[3], lightDark: true}
			} else {
				decls[d[1]] = toneColour{day: hexOf(d[2]), night: hexOf(d[2]), dayValue: d[2]}
			}
		}
		blocks[m[1]] = decls
	}
	return blocks
}

// TestAppCSSToneColoursConsumed checks that every tone colour app.css declares has
// a consumer. A contrast guarantee is useful only for a colour the stylesheet
// draws.
func TestAppCSSToneColoursConsumed(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	consumers := regexp.MustCompile(`var\(\s*(--tone-[a-z]+)\s*[,)]`)
	used := make(map[string]bool)
	for _, match := range consumers.FindAllStringSubmatch(string(sheet), -1) {
		used[match[1]] = true
	}
	for _, block := range toneBlock.FindAllStringSubmatch(string(sheet), -1) {
		for _, decl := range toneDecl.FindAllStringSubmatch(block[2], -1) {
			if !used[decl[1]] {
				t.Errorf("data-tone=%q declares %s without a stylesheet consumer", block[1], decl[1])
			}
		}
	}
}

// periodOverride finds the colours a head gives the period on its tone, and
// periodDecl one of them: a tone token, a token of the page's own, or a
// translucent rgb().
var (
	periodOverride = regexp.MustCompile(`(?s)\.(goen-pagehead)\[data-tone(?:="([a-z]+)")?\] \.ui-period \{(.*?)\}`)
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

// toneHeads are the heads that lay the period on a tone's ground: the
// department and campaign head. The home hero draws its days on the page, beside
// the words that say them, and hides them from a screen reader.
var toneHeads = []string{"goen-pagehead"}

// toneNames is tone.Tone's closed set, as the data-tone attribute spells it.
var toneNames = func() []string {
	var names []string
	for v := range tone.All() {
		names = append(names, string(v))
	}
	return names
}()

// TestAppCSSToneGroundContrast checks that every tone ground holds its text. It
// holds the text a department or campaign head shows to 4.5:1 on each tone's
// ground, by day and by night, the vermilion of a sale's price and end on each
// light tone, which the home band lays them on, and its period fill to 3:1
// (WCAG 1.4.11). A new tone is a new block, and
// one whose ground drifts from its oklch source would fail a reader without any
// route the axe gate visits showing it.
func TestAppCSSToneGroundContrast(t *testing.T) {
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

	blocks := toneBlocks(t, string(sheet), tokens, aliasTokens(t))
	vermilion, ok := roleTokens(t)["--sys-tertiary"]
	if !ok {
		t.Fatalf("%s declares no --sys-tertiary", BaseCSS)
	}
	if len(blocks) != len(toneNames) {
		t.Errorf("%s has %d data-tone blocks, want the %d of the closed set", AppCSS, len(blocks), len(toneNames))
	}

	for _, name := range toneNames {
		decl := blocks[name]
		for _, prop := range []string{"--tone-ground", "--tone-text", "--tone-muted", "--tone-mark"} {
			if len(decl[prop].day) != 6 || len(decl[prop].night) != 6 {
				t.Fatalf("data-tone=%q declares no colour for %s", name, prop)
			}
		}
		ground := decl["--tone-ground"].day
		// The mark is also the colour of a link on the tone ("see all").
		for _, prop := range []string{"--tone-text", "--tone-muted", "--tone-mark"} {
			if got := contrast(decl[prop].day, ground); got < 4.5 {
				t.Errorf("%s (#%s) on the %s ground (#%s) = %.2f:1, want at least 4.5:1",
					prop, decl[prop].day, name, ground, got)
			}
		}
		// By night a tone that kept its day ground would be a lit block on a
		// dark page, so each ground has a night of its own, and the text on it
		// is held as by day. The period is held by day only: its track,
		// --period-track, has no night value yet.
		if !decl["--tone-ground"].lightDark {
			t.Errorf("data-tone=%q declares --tone-ground without light-dark(), so the night draws the day's ground", name)
		}
		for _, prop := range []string{"--tone-text", "--tone-muted", "--tone-mark"} {
			night := decl["--tone-ground"].night
			if got := contrast(decl[prop].night, night); got < 4.5 {
				t.Errorf("night: %s (#%s) on the %s ground (#%s) = %.2f:1, want at least 4.5:1",
					prop, decl[prop].night, name, night, got)
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
						period[d[1]] = decl[d[2]].day
					case rgba[d[2]] != "":
						period[d[1]] = rgba[d[2]]
					default:
						period[d[1]] = tokens[d[2]]
					}
				}
			}
			checkPeriod(t, head, period["fill"], period["track"], "the "+name+" ground", ground)
		}
		if name == "ink" {
			continue
		}
		// The band's sale price and its last day are vermilion on every light
		// tone; the ink band draws them in its text colour instead.
		for _, scheme := range []struct{ name, ink, ground string }{
			{"day", vermilion.day, decl["--tone-ground"].day},
			{"night", vermilion.night, decl["--tone-ground"].night},
		} {
			if got := contrast(scheme.ink, scheme.ground); got < 4.5 {
				t.Errorf("%s: --sys-tertiary (#%s) on the %s ground (#%s) = %.2f:1, want at least 4.5:1",
					scheme.name, scheme.ink, name, scheme.ground, got)
			}
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

// toneFallback finds the tone rules a browser without light-dark() reads
// instead, and fallbackDecl one declaration in them.
var (
	toneFallback = regexp.MustCompile(`(?s)@supports not \(color: light-dark\(#000, #fff\)\) \{\s*((?:\[data-tone="[a-z]+"\]\s*\{[^{}]*\}\s*)+)\}`)
	fallbackRule = regexp.MustCompile(`\[data-tone="([a-z]+)"\]\s*\{([^{}]*)\}`)
	fallbackDecl = regexp.MustCompile(`(--tone-[a-z]+):\s*([^;]+);`)
)

// TestAppCSSToneFallback checks that a browser without light-dark() draws each
// tone by day. A tone colour declared with light-dark() is invalid there, and
// a rule reading it falls back to its initial colour: a band with no ground,
// or black text where the ink tone wants white.
func TestAppCSSToneFallback(t *testing.T) {
	t.Parallel()

	app, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	sheet := cssComment.ReplaceAllString(string(app), "")
	block := toneFallback.FindStringSubmatch(sheet)
	if block == nil {
		t.Fatalf("%s has no @supports not (color: light-dark(#000, #fff)) rule for the tones", AppCSS)
	}
	fallback := make(map[string]map[string]string)
	for _, r := range fallbackRule.FindAllStringSubmatch(block[1], -1) {
		decls := make(map[string]string)
		for _, d := range fallbackDecl.FindAllStringSubmatch(r[2], -1) {
			decls[d[1]] = strings.TrimSpace(d[2])
		}
		fallback[r[1]] = decls
	}

	tones := toneBlocks(t, sheet, hexTokens(t), aliasTokens(t))
	for name, decls := range tones {
		for prop, c := range decls {
			got, set := fallback[name][prop]
			switch {
			case c.lightDark && got != c.dayValue:
				t.Errorf("without light-dark(), data-tone=%q %s = %q, want %q, its day value", name, prop, got, c.dayValue)
			case !c.lightDark && set:
				t.Errorf("the light-dark() fallback sets data-tone=%q %s, which the tone declares without light-dark()", name, prop)
			}
		}
	}
	for name, decls := range fallback {
		for prop := range decls {
			if _, ok := tones[name][prop]; !ok {
				t.Errorf("the light-dark() fallback sets data-tone=%q %s, which the tone does not declare", name, prop)
			}
		}
	}
}

// TestAppCSSStarPickerContrast checks that the star picker is visible on the
// review form. The review form's star picker is a control with no text, so WCAG
// 1.4.11 holds its outline and its selected fill to 3:1 against the ground the
// form sits on, and the fill must stand out from the outline around it.
func TestAppCSSStarPickerContrast(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	tokens := hexTokens(t)
	// The picker is drawn by day only: its outline, --n-900, has no night.
	for name, r := range lightDarkTokens(t) {
		tokens[name] = r.day
	}
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

// TestBaseCSSControlBoundary checks that control boundaries read on their grounds.
func TestBaseCSSControlBoundary(t *testing.T) {
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

// TestAppCSSFocusRingContrast checks that the focus ring reads on every ground.
// The focus ring is a graphical object (WCAG 1.4.11, 2.4.7): 3:1 on every ground a
// focusable thing sits on. The accent is dark, so the grounds that are dark
// themselves re-point --ring to white.
func TestAppCSSFocusRingContrast(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	tokens := hexTokens(t)

	for _, ground := range []string{"--n-0", "--n-50", "--wash", "--well"} {
		if got := contrast(tokens["--accent"], tokens[ground]); math.IsNaN(got) || got < 3 {
			t.Errorf("focus ring --accent (#%s) on %s (#%s) = %.2f:1, want at least 3:1",
				tokens["--accent"], ground, tokens[ground], got)
		}
	}

	// A rule that re-points --ring either paints a ground of its own from a
	// role, and its ring is held on that ground in both schemes, or it is the
	// rule that whitens the ring on the dark grounds.
	roles := roleTokens(t)
	ringDecl := regexp.MustCompile(`(?:^|;)\s*--ring:\s*var\((--[a-z0-9-]+)\);`)
	roleGround := regexp.MustCompile(`(?:^|;)\s*background:\s*var\((--sys-[a-z0-9-]+)\);`)
	dark := 0
	for _, r := range cssRule.FindAllStringSubmatch(cssComment.ReplaceAllString(string(sheet), ""), -1) {
		decl := ringDecl.FindStringSubmatch(";" + r[2])
		if decl == nil {
			continue
		}
		selectors := strings.Join(selectorList(r[1]), ", ")
		if _, isRole := roles[decl[1]]; isRole {
			g := roleGround.FindStringSubmatch(";" + r[2])
			if len(g) < 2 || roles[g[1]].day == "" {
				t.Errorf("%s re-points --ring to the role %s without painting a role ground of its own to hold it on", selectors, decl[1])
				continue
			}
			ring, ground := roles[decl[1]], roles[g[1]]
			for _, s := range []struct{ name, ring, ground string }{{"day", ring.day, ground.day}, {"night", ring.night, ground.night}} {
				if got := contrast(s.ring, s.ground); got < 3 {
					t.Errorf("%s: the focus ring of %s, %s (#%s), on its ground %s (#%s) = %.2f:1, want at least 3:1",
						s.name, selectors, decl[1], s.ring, g[1], s.ground, got)
				}
			}
			continue
		}
		dark++
		for _, sel := range selectorList(r[1]) {
			// A bare tone selector also reaches the light grounds that carry
			// the tone as data, where a white ring would be 1:1.
			if strings.HasPrefix(sel, "[data-tone") {
				t.Errorf("the white --ring override selects %q on any element; scope it to a dark ground", sel)
			}
		}
		for _, bg := range []string{tokens["--n-900"], "18181b" /* [data-tone="ink"] */} {
			if got := contrast(tokens[decl[1]], bg); math.IsNaN(got) || got < 3 {
				t.Errorf("focus ring %s (#%s) on the dark ground #%s = %.2f:1, want at least 3:1",
					decl[1], tokens[decl[1]], bg, got)
			}
		}
	}
	if dark == 0 {
		t.Fatal("app.css does not re-point --ring on any dark ground")
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

// cssRule finds a rule whose block holds no other block: a top-level rule, or
// one inside @media.
var cssRule = regexp.MustCompile(`([^{}]*)\{([^{}]*)\}`)

// selectorList splits a rule's selector list at its top-level commas, so a
// comma inside :not() or :is() stays with its selector.
func selectorList(list string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range list {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(list[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(list[start:]))
}

// TestAppCSSFilledControlFocusHalo checks that a filled action control parts its
// focus ring from its fill with a band of the ground it sits on. The ring is
// 1.21:1 on the night's warm-stone button, so without the band the focus is not
// seen. Each selector that fills a control with an action colour must have a
// :focus-visible rule drawing the ring (outline: 2px solid var(--ring)) and a
// box-shadow of --sys-surface, or of --tone-ground where the control sits on a
// tone.
func TestAppCSSFilledControlFocusHalo(t *testing.T) {
	t.Parallel()

	app, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	rules := cssRule.FindAllStringSubmatch(cssComment.ReplaceAllString(string(app), ""), -1)
	filled := regexp.MustCompile(`(?:^|;)\s*background(?:-color)?:\s*var\(--(?:sys|day|night)-action\)\s*;`)
	ring := regexp.MustCompile(`(?:^|;)\s*outline:\s*2px solid var\(--ring\)\s*;`)
	halo := regexp.MustCompile(`(?:^|;)\s*box-shadow:\s*0 0 0 [1-9]\d*px var\((--[a-z0-9-]+)\)\s*;`)

	decls := make(map[string]string)
	for _, r := range rules {
		for _, sel := range selectorList(r[1]) {
			decls[sel] += ";" + r[2]
		}
	}
	found := 0
	for _, r := range rules {
		if !filled.MatchString(";" + r[2]) {
			continue
		}
		for _, sel := range selectorList(r[1]) {
			found++
			focus := sel + ":focus-visible"
			want := "--sys-surface"
			if strings.Contains(sel, "[data-tone") {
				want = "--tone-ground"
			}
			if !ring.MatchString(decls[focus]) {
				t.Errorf("%s: %s draws no 2px solid var(--ring) outline", AppCSS, focus)
			}
			if m := halo.FindStringSubmatch(decls[focus]); len(m) < 2 || m[1] != want {
				t.Errorf("%s: %s has no box-shadow band of var(%s) between the ring and the fill", AppCSS, focus, want)
			}
		}
	}
	if found == 0 {
		t.Fatalf("%s fills no control with an action colour; the check found nothing to hold", AppCSS)
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

// TestAppCSSBandMutedText checks that the band reads its muted text from the tone.
// The band paints its tone behind text that the page styles in --n-500, which no
// tone ground holds to 4.5:1; each of these classes must take the tone's own muted
// colour.
func TestAppCSSBandMutedText(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	rule := regexp.MustCompile(`(?s)((?:\.goen-band [.a-z_-]+,\s*)*\.goen-band [.a-z_-]+) \{\s*color: var\(--tone-muted\);`).FindStringSubmatch(string(sheet))
	if rule == nil {
		t.Fatalf("%s has no band rule setting color: var(--tone-muted)", AppCSS)
	}
	for _, class := range []string{"goen-band__fact", "goen-tile__was", "goen-tile__state", "goen-tile__colours"} {
		if !strings.Contains(rule[1], ".goen-band ."+class) {
			t.Errorf("the band's muted-text rule does not name .%s", class)
		}
	}
}

// TestAppCSSPromoDeclaredOnce checks that .goen-promo is declared only for the
// promotion strip. The promotion strip owns .goen-promo; a second block declaring
// it restyles the strip on every page that has one.
func TestAppCSSPromoDeclaredOnce(t *testing.T) {
	t.Parallel()
	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(regexp.MustCompile(`(?m)^\.goen-promo \{`).FindAll(sheet, -1)); got != 1 {
		t.Errorf(".goen-promo is declared %d times, want once, as the strip", got)
	}
	// The strip is a notification, drawn on the inverse roles with the inverse
	// blue for its ring, and no other rule repaints it or re-points its ring.
	block := regexp.MustCompile(`(?m)^\.goen-promo \{([^{}]*)\}`).FindSubmatch(sheet)
	if block == nil {
		t.Fatalf("%s has no .goen-promo rule", AppCSS)
	}
	for _, want := range []string{
		"--ring: var(--sys-inverse-primary);",
		"background: var(--sys-inverse-surface);",
		"color: var(--sys-inverse-on-surface);",
	} {
		if !strings.Contains(string(block[1]), want) {
			t.Errorf("%s: .goen-promo does not declare %q", AppCSS, want)
		}
	}
	for _, r := range cssRule.FindAllStringSubmatch(cssComment.ReplaceAllString(string(sheet), ""), -1) {
		if !slices.Contains(selectorList(r[1]), ".goen-promo") || strings.TrimSpace(r[1]) == ".goen-promo" {
			continue
		}
		if regexp.MustCompile(`(?:^|;)\s*(?:--ring|background|color)\s*:`).MatchString(";" + r[2]) {
			t.Errorf("%s: %q repaints the strip or re-points its ring; the strip's own rule holds them", AppCSS, strings.Join(selectorList(r[1]), ", "))
		}
	}
}

// TestAppCSSInverseNoticeParts checks the parts drawn on the inverse ground:
// the promotional strip's code, its dismiss control and the link of the notice
// that confirms an action. The text, the link and the ring on the inverse
// ground are held by TestBaseCSSRoleContrast; this holds what the rules lay on
// it. The code's edge is the text colour, so it holds what the text holds. The
// dismiss control lays the text colour over the ground when hovered and
// pressed, and its glyph, a graphical object, holds 3:1 on what that leaves
// (WCAG 1.4.11) by day and by night. The confirming notice paints the same
// ground, and its link is the inverse blue, underlined, with an inverse band
// outside its focus ring.
func TestAppCSSInverseNoticeParts(t *testing.T) {
	t.Parallel()

	app, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	sheet := cssComment.ReplaceAllString(string(app), "")
	roles := roleTokens(t)
	layers := stateLayers(t)
	decls := make(map[string]string)
	for _, r := range cssRule.FindAllStringSubmatch(sheet, -1) {
		for _, sel := range selectorList(r[1]) {
			decls[sel] += ";" + r[2]
		}
	}
	has := func(sel, decl string) {
		t.Helper()
		if !regexp.MustCompile(`(?:^|;)\s*` + regexp.QuoteMeta(decl)).MatchString(decls[sel]) {
			t.Errorf("%s: %s does not declare %q", AppCSS, sel, decl)
		}
	}

	has(".goen-promo__code", "border: 1px solid currentcolor;")
	if regexp.MustCompile(`(?:^|;)\s*(?:background|color)\s*:`).MatchString(decls[".goen-promo__code"]) {
		t.Errorf("%s: .goen-promo__code paints a colour of its own; it takes the strip's", AppCSS)
	}

	text, ground := roles["--sys-inverse-on-surface"], roles["--sys-inverse-surface"]
	for _, state := range []string{"hover", "active"} {
		sel := ".goen-promo__close:" + state
		layer := map[string]string{"hover": "--state-hover", "active": "--state-press"}[state]
		has(sel, "background: color-mix(in srgb, currentcolor var("+layer+"), transparent);")
		for _, s := range []struct{ name, text, ground string }{{"day", text.day, ground.day}, {"night", text.night, ground.night}} {
			seen := mix(s.text, s.ground, layers[layer])
			if got := contrast(s.text, seen); got < 3 {
				t.Errorf("%s: the dismiss glyph (#%s) on %s (#%s) = %.2f:1, want at least 3:1", s.name, s.text, sel, seen, got)
			}
		}
	}

	for _, sel := range []string{".goen-pdp__added.goen-notice--accent", ".goen-pdp__notify > .goen-notice--accent"} {
		has(sel, "--ring: var(--sys-inverse-primary);")
		has(sel, "background: var(--sys-inverse-surface);")
		has(sel, "color: var(--sys-inverse-on-surface);")
	}
	for _, sel := range []string{".goen-pdp__added .goen-pdp__addedlink", ".goen-pdp__added .goen-pdp__addedlink:hover"} {
		has(sel, "color: var(--sys-inverse-primary);")
		has(sel, "text-decoration: underline;")
	}
	// Focused, the link's ring is the inverse blue the notice sets as --ring, with a band of the
	// inverse ground outside it, not the page's.
	has(".goen-pdp__added .goen-pdp__addedlink:focus-visible", "outline: 2px solid var(--ring);")
	has(".goen-pdp__added .goen-pdp__addedlink:focus-visible", "box-shadow: 0 0 0 4px var(--sys-inverse-surface);")
}

// TestAppCSSDepartmentPanelGround checks the department panel's ground. By day
// it is the page's own white and the shadow alone sets it off; by night the
// page's ground under it would make it read as more of the page, so it is a
// step lighter, at least 1.25:1 on the page, and keeps the shadow. Without
// light-dark() the panel is the page's surface.
func TestAppCSSDepartmentPanelGround(t *testing.T) {
	t.Parallel()

	app, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	sheet := cssComment.ReplaceAllString(string(app), "")
	var bodies []string
	for _, r := range cssRule.FindAllStringSubmatch(sheet, -1) {
		if slices.Contains(selectorList(r[1]), ".goen-dept__panel") {
			bodies = append(bodies, r[2])
		}
	}
	decls := strings.Join(bodies, ";")
	for _, want := range []string{"background: var(--sys-surface);", "box-shadow: var(--sh-lg);"} {
		if !strings.Contains(decls, want) {
			t.Errorf("%s: .goen-dept__panel does not declare %q", AppCSS, want)
		}
	}
	m := regexp.MustCompile(`background: light-dark\(var\((--[a-z0-9-]+)\), var\((--[a-z0-9-]+)\)\);`).FindStringSubmatch(decls)
	if m == nil {
		t.Fatalf("%s: .goen-dept__panel has no background: light-dark(var(--day), var(--night))", AppCSS)
	}
	tokens, links := hexTokens(t), aliasTokens(t)
	page := roleTokens(t)["--sys-surface"]
	if day := resolve(m[1], tokens, links); day != page.day {
		t.Errorf("day: the panel is %s (#%s), want the page's white (#%s)", m[1], day, page.day)
	}
	night := resolve(m[2], tokens, links)
	if got := contrast(night, page.night); math.IsNaN(got) || got < 1.25 {
		t.Errorf("night: the panel %s (#%s) on the page (#%s) = %.2f:1, want at least 1.25:1", m[2], night, page.night, got)
	}
}

// TestFieldBoundariesHoldContrast checks that the boundaries of invalid and
// read-only fields hold their contrast. The state colours are read from the rules
// that actually paint the boundary.
func TestFieldBoundariesHoldContrast(t *testing.T) {
	t.Parallel()
	tokens := hexTokens(t)
	links := aliasTokens(t)
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
		colour := resolve(match[1], tokens, links)
		if colour == "" {
			t.Fatalf("%s: cannot resolve %s to a hex colour", state.name, match[1])
		}
		for _, ground := range []string{"--n-0", "--n-50"} {
			got := contrast(colour, tokens[ground])
			if math.IsNaN(got) || got < 3 {
				t.Errorf("%s %s (#%s) on %s (#%s) = %.2f:1, want at least 3:1", state.name, match[1], colour, ground, tokens[ground], got)
			}
		}
	}
}

// cssComment is stripped before selectors are read, so a comment above a rule
// is not taken for part of its selector.
var cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

// TestAppCSSShelfPriceContrast checks that shelf prices read on their grounds. A
// shelf price is one token, --price: the ink blue by day and the text colour by
// night, where the blue is the link's. It is held to 4.5:1 on every ground a
// shelf price sits on in each scheme. The ink band is the one ground the
// blue fails on, so it alone re-points the token to the tone's own text colour,
// and no other rule may declare it: a bare [data-tone="ink"] would white-out the
// price on the light grounds that carry the tone as data.
func TestAppCSSShelfPriceContrast(t *testing.T) {
	t.Parallel()

	tokens := hexTokens(t)
	links := aliasTokens(t)
	schemed, ok := lightDarkTokens(t)["--price"]
	if !ok {
		t.Fatalf("%s does not declare --price as light-dark(var(--day), var(--night))", BaseCSS)
	}
	price := schemed.day

	app, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	sheet := cssComment.ReplaceAllString(string(app), "")
	tones := toneBlocks(t, sheet, tokens, links)

	grounds := map[string]string{}
	for _, name := range []string{"--n-0", "--n-50", "--wash", "--well"} {
		grounds[name] = tokens[name]
	}
	for _, name := range toneNames {
		if name != "ink" {
			grounds["the "+name+" tone"] = tones[name]["--tone-ground"].day
		}
	}
	for name, ground := range grounds {
		if got := contrast(price, ground); math.IsNaN(got) || got < 4.5 {
			t.Errorf("--price (#%s) on %s (#%s) = %.2f:1, want at least 4.5:1", price, name, ground, got)
		}
	}
	nightGrounds := map[string]string{}
	for name, r := range roleTokens(t) {
		if strings.HasPrefix(name, "--sys-surface") {
			nightGrounds[name] = r.night
		}
	}
	for _, name := range toneNames {
		if name != "ink" {
			nightGrounds["the "+name+" tone"] = tones[name]["--tone-ground"].night
		}
	}
	if len(nightGrounds) < 6 {
		t.Fatalf("found %d night grounds for --price, want the surfaces and the light tones", len(nightGrounds))
	}
	for name, ground := range nightGrounds {
		if got := contrast(schemed.night, ground); math.IsNaN(got) || got < 4.5 {
			t.Errorf("night: --price (%s #%s) on %s (#%s) = %.2f:1, want at least 4.5:1",
				schemed.nightToken, schemed.night, name, ground, got)
		}
	}

	// Only the ink band re-points it, to a colour that reads on the ink ground.
	var declared []string
	for _, rule := range regexp.MustCompile(`([^{}]*)\{([^{}]*)\}`).FindAllStringSubmatch(sheet, -1) {
		if regexp.MustCompile(`--price\s*:`).MatchString(rule[2]) {
			declared = append(declared, strings.TrimSpace(rule[1]))
		}
	}
	const inkBand = `.goen-band[data-tone="ink"]`
	if len(declared) != 1 || declared[0] != inkBand {
		t.Fatalf("%s declares --price in %q, want only in %s", AppCSS, declared, inkBand)
	}
	override := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(inkBand) + `\s*\{\s*--price:\s*var\((--tone-[a-z]+)\);\s*\}`).FindStringSubmatch(sheet)
	if override == nil {
		t.Fatalf("%s: the ink band does not point --price at a tone colour", AppCSS)
	}
	ink := tones["ink"]
	for _, scheme := range []struct {
		name   string
		colour func(toneColour) string
	}{
		{"day", func(c toneColour) string { return c.day }},
		{"night", func(c toneColour) string { return c.night }},
	} {
		text, ground := scheme.colour(ink[override[1]]), scheme.colour(ink["--tone-ground"])
		if got := contrast(text, ground); math.IsNaN(got) || got < 4.5 {
			t.Errorf("%s: --price on the ink band is %s (#%s) on the ink ground (#%s) = %.2f:1, want at least 4.5:1",
				scheme.name, override[1], text, ground, got)
		}
	}

	for _, sheetName := range []string{BaseCSS, AdminCSS} {
		body, err := fs.ReadFile(files, sheetName)
		if err != nil {
			t.Fatalf("read %s: %v", sheetName, err)
		}
		want := 0
		if sheetName == BaseCSS {
			// The light-dark() fallback is held by TestBaseCSSRoleFallback.
			body = roleFallback.ReplaceAll(body, nil)
			want = 1
		}
		if got := len(regexp.MustCompile(`(?m)^\s*--price\s*:`).FindAll(body, -1)); got != want {
			t.Errorf("%s declares --price %d times, want %d", sheetName, got, want)
		}
	}

	// The shelf, the product page, the buy bar and the department's picks read
	// the token, and nothing in the band takes the price back to the tone's text.
	for _, class := range []string{"goen-tile__price", "goen-pdp__price", "goen-buybar__price", "goen-dept__price"} {
		if !regexp.MustCompile(`(?s)\.` + class + ` \{[^{}]*\bcolor: var\(--price\);`).MatchString(sheet) {
			t.Errorf("%s: .%s does not take color: var(--price)", AppCSS, class)
		}
	}
	if regexp.MustCompile(`\.goen-band [^{}]*\.goen-tile__price[^{}]*\{[^{}]*color: var\(--tone-text\)`).MatchString(sheet) {
		t.Errorf("%s: a band rule sets a tile price to --tone-text, which on a light tone hides the blue", AppCSS)
	}

	// A sold-out price steps back to grey: --n-500 on white, and on a band the
	// tone's muted colour, because --n-500 is under 4.5:1 on four of the five
	// light tones.
	out := regexp.MustCompile(`(?ms)^\.goen-tile--out \.goen-tile__price \{\s*color: var\((--[a-z0-9-]+)\);`).FindStringSubmatch(sheet)
	if out == nil {
		t.Fatalf("%s has no sold-out price rule", AppCSS)
	}
	grey := resolve(out[1], tokens, links)
	for _, name := range []string{"--n-0", "--n-50", "--wash", "--well"} {
		if got := contrast(grey, tokens[name]); math.IsNaN(got) || got < 4.5 {
			t.Errorf("a sold-out price %s (#%s) on %s (#%s) = %.2f:1, want at least 4.5:1", out[1], grey, name, tokens[name], got)
		}
	}
	if !regexp.MustCompile(`(?s)\.goen-band \.goen-tile--out \.goen-tile__price \{\s*color: var\(--tone-muted\);`).MatchString(sheet) {
		t.Errorf("%s: a sold-out price on a band does not take the tone's muted colour", AppCSS)
	}
}
