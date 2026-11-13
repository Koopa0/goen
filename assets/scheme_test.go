package assets

import (
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// schemed is a colour as the storefront draws it in each scheme, as six hex
// digits, or "" on a side the expression does not resolve to one opaque colour.
type schemed struct {
	day, night string
}

// lightDarkFallback finds the opening of a rule a browser without light-dark()
// reads instead.
var lightDarkFallback = regexp.MustCompile(`@supports not \(color: light-dark\(#000, #fff\)\)\s*\{`)

// withoutBlocks returns sheet without each block that open finds, braces
// balanced, so a rule inside one is not read as the sheet's own.
func withoutBlocks(sheet string, open *regexp.Regexp) string {
	var b strings.Builder
	for {
		loc := open.FindStringIndex(sheet)
		if loc == nil {
			b.WriteString(sheet)
			return b.String()
		}
		b.WriteString(sheet[:loc[0]])
		depth, end := 1, loc[1]
		for ; end < len(sheet) && depth > 0; end++ {
			switch sheet[end] {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		sheet = sheet[end:]
	}
}

// rootDecl finds one custom property declaration in a :root block.
var rootDecl = regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*([^;]+);`)

// storefrontTokens reads every custom property the storefront declares on
// :root, base.css's first and app.css's over them, as each browser that has
// light-dark() resolves it by day and by night. The light-dark() fallback is
// left out: it is the day, and TestBaseCSSRoleFallback and
// TestAppCSSPairFallback hold it.
func storefrontTokens(t *testing.T) map[string]schemed {
	t.Helper()
	decls := make(map[string]string)
	for _, name := range []string{BaseCSS, AppCSS} {
		body, err := fs.ReadFile(files, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		sheet := withoutBlocks(cssComment.ReplaceAllString(string(body), ""), lightDarkFallback)
		for _, r := range cssRule.FindAllStringSubmatch(sheet, -1) {
			if strings.TrimSpace(r[1]) != ":root" {
				continue
			}
			for _, d := range rootDecl.FindAllStringSubmatch(r[2], -1) {
				decls[d[1]] = strings.TrimSpace(d[2])
			}
		}
	}
	out := make(map[string]schemed, len(decls))
	for name := range decls {
		out[name] = schemed{
			day:   evalColour("var("+name+")", decls, false, 0),
			night: evalColour("var("+name+")", decls, true, 0),
		}
	}
	return out
}

var (
	hexColour  = regexp.MustCompile(`^#([0-9a-fA-F]{6})$`)
	varRef     = regexp.MustCompile(`^var\((--[a-z0-9-]+)\)$`)
	cssCall    = regexp.MustCompile(`^([a-z-]+)\((.*)\)$`)
	mixPercent = regexp.MustCompile(`^(.*\S)\s+(\d+(?:\.\d+)?%|var\(--[a-z0-9-]+\))$`)
)

// evalColour resolves a colour expression of the forms the sheets write: a
// six-digit hex, var(), light-dark() and color-mix(in srgb, …) whose
// percentage may itself be a var(). Anything else is "".
func evalColour(expr string, decls map[string]string, night bool, depth int) string {
	expr = strings.TrimSpace(expr)
	if depth > 20 {
		return ""
	}
	if m := hexColour.FindStringSubmatch(expr); m != nil {
		return strings.ToLower(m[1])
	}
	if m := varRef.FindStringSubmatch(expr); m != nil {
		v, ok := decls[m[1]]
		if !ok {
			return ""
		}
		return evalColour(v, decls, night, depth+1)
	}
	m := cssCall.FindStringSubmatch(expr)
	if m == nil {
		return ""
	}
	args := selectorList(m[2])
	switch {
	case m[1] == "light-dark" && len(args) == 2:
		if night {
			return evalColour(args[1], decls, night, depth+1)
		}
		return evalColour(args[0], decls, night, depth+1)
	case m[1] == "color-mix" && len(args) == 3 && args[0] == "in srgb":
		p := mixPercent.FindStringSubmatch(args[1])
		if p == nil {
			return ""
		}
		pct := p[2]
		if v := varRef.FindStringSubmatch(pct); v != nil {
			pct = strings.TrimSpace(decls[v[1]])
		}
		f, err := strconv.ParseFloat(strings.TrimSuffix(pct, "%"), 64)
		if err != nil {
			return ""
		}
		return mix(evalColour(p[1], decls, night, depth+1), evalColour(args[2], decls, night, depth+1), f/100)
	}
	return ""
}

// dayOnlyColour finds a value that names a colour of its own: a hex, an
// escaped hex inside a data: URI, or rgb().
var dayOnlyColour = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|%23[0-9a-fA-F]{3,8}\b|\brgba?\(`)

// dayOnlyTokens are base.css's colour tokens that have no night: every :root
// token whose value is or leads to a colour of its own, without light-dark().
// A --day-* token is the day half of a role, and as day-only as the ramp; the
// --night-* halves, the roles and the back office's --chart-* are not.
func dayOnlyTokens(t *testing.T) map[string]bool {
	t.Helper()
	body, err := fs.ReadFile(files, BaseCSS)
	if err != nil {
		t.Fatalf("read %s: %v", BaseCSS, err)
	}
	sheet := withoutBlocks(cssComment.ReplaceAllString(string(body), ""), lightDarkFallback)
	decls := make(map[string]string)
	for _, r := range cssRule.FindAllStringSubmatch(sheet, -1) {
		if strings.TrimSpace(r[1]) == ":root" {
			for _, d := range rootDecl.FindAllStringSubmatch(r[2], -1) {
				decls[d[1]] = d[2]
			}
		}
	}
	var isDay func(name string, depth int) bool
	isDay = func(name string, depth int) bool {
		v, ok := decls[name]
		if !ok || depth > 20 || strings.Contains(v, "light-dark(") {
			return false
		}
		if dayOnlyColour.MatchString(v) {
			return true
		}
		for _, m := range customUse.FindAllStringSubmatch(v, -1) {
			if isDay(m[1], depth+1) {
				return true
			}
		}
		return false
	}
	out := make(map[string]bool)
	for name := range decls {
		if regexp.MustCompile(`^--(?:night|sys|chart)-`).MatchString(name) {
			continue
		}
		if isDay(name, 0) {
			out[name] = true
		}
	}
	return out
}

// schemeException is a place app.css may draw a colour that does not follow
// the scheme: the declaration as written, in a rule whose selector list
// contains selector.
type schemeException struct {
	selector, decl, why string
}

// schemeExceptions is short on purpose, and each row has to match a
// declaration, so one left behind by a rename fails.
var schemeExceptions = []schemeException{
	{`[data-tone="paper"]`, `light-dark(#eae1d6, #2b2722)`, "a tone is the design's named palette: day and night are both written out, and TestAppCSSToneGroundContrast measures them"},
	{`[data-tone="paper"]`, `light-dark(var(--n-900), #e8e8e6)`, "a tone's text, named with its ground"},
	{`[data-tone="paper"]`, `light-dark(#4d4d53, #b8b8b5)`, "a tone's muted text, named with its ground"},
	{`[data-tone="stone"]`, `light-dark(#e6e1d9, #29261f)`, "the stone tone's ground"},
	{`[data-tone="stone"]`, `light-dark(var(--n-900), #e8e8e6)`, "a tone's text"},
	{`[data-tone="stone"]`, `light-dark(#4d4d53, #b8b8b5)`, "a tone's muted text"},
	{`[data-tone="mist"]`, `light-dark(#d4e3f2, #1f2933)`, "the mist tone's ground"},
	{`[data-tone="mist"]`, `light-dark(var(--n-900), #e8e8e6)`, "a tone's text"},
	{`[data-tone="mist"]`, `light-dark(#4d4d53, #b8b8b5)`, "a tone's muted text"},
	{`[data-tone="sage"]`, `light-dark(#d5e2d2, #1f2621)`, "the sage tone's ground"},
	{`[data-tone="sage"]`, `light-dark(var(--n-900), #e8e8e6)`, "a tone's text"},
	{`[data-tone="sage"]`, `light-dark(#4d4d53, #b8b8b5)`, "a tone's muted text"},
	{`[data-tone="blush"]`, `light-dark(#f1d9d6, #2b2224)`, "the blush tone's ground"},
	{`[data-tone="blush"]`, `light-dark(var(--n-900), #e8e8e6)`, "a tone's text"},
	{`[data-tone="blush"]`, `light-dark(#4d4d53, #b8b8b5)`, "a tone's muted text"},
	{`[data-tone="ink"]`, `#ffffff`, "text on the ink tone, which is dark in both schemes"},
	{`[data-tone="ink"]`, `#a1a1aa`, "muted text on the ink tone, dark in both schemes"},
	{`[data-tone="ink"]`, `var(--on-ink-accent)`, "the blue on the ink tone, dark in both schemes"},
	{`.goen-band[data-tone="ink"]`, `var(--n-0)`, "the white ring on the ink tone, dark in both schemes"},
	{`.goen-pagehead[data-tone="ink"] .ui-period`, `var(--period-track-on-ink)`, "the period's track on the ink tone, dark in both schemes"},
	{`.goen-hero__tabs`, `mask-image: linear-gradient(to right, #000`, "a mask's alpha, not a colour drawn"},
	{`:root`, `--chevron-scheme: var(--chevron)`, "the day's select arrow; the night's is set under prefers-color-scheme: dark"},
	{`:root`, `%23b0b0b8`, "the night's select arrow, --night-on-surface-variant written out: no custom property reaches into a data: URI"},
	{`.goen-filters__prices`, `rgb(0 0 0 / 0.2)`, "a shadow: black in both schemes, where by night the surface's own step does the lifting"},
	{`.goen-dept__panel`, `var(--sh-lg)`, "a shadow: black in both schemes; by night the panel is also a step lighter than the page"},
	{`.goen-langmenu__panel`, `var(--sh-lg)`, "a shadow: black in both schemes"},
	{`.goen-header__drawer`, `var(--sh-xl)`, "a shadow: black in both schemes"},
	{`.goen-header__menu[open]::after`, `rgb(0 0 0 / 0.4)`, "the scrim: black over either ground"},
	{`:root`, `light-dark(var(--period-track), var(--period-track-on-ink))`, "the night's track is the ink tone's, the night's blue at the day track's 30%"},
	{`:root`, `light-dark(var(--n-900), var(--n-0))`, "the star picker's outline by night is white: see .goen-pdp__starmark"},
	{`:root`, `color-mix(in srgb, var(--success) 40%, var(--night-on-surface))`, "the night's green is the day's raised toward the night's text"},
	{`.goen-pdp__starrow:hover .goen-pdp__star:hover .goen-pdp__starmark svg`, `fill: var(--day-star)`, "the picker keeps the day's deeper amber by night, the one amber 3:1 on both its white outline and the dark ground"},
}

// TestAppCSSColoursFollowTheScheme checks that the storefront sheet draws no
// colour that stays the day's by night. A colour written out (a hex, an
// escaped hex in a data: URI, rgb()) or a base.css token with no night value
// (the --n-* ramp, --ink, --line, --accent-*, --error and the rest) is allowed
// only as the day half of a light-dark(), inside the light-dark() fallback, or
// in a declaration schemeExceptions names with its reason. Anywhere else the
// night would draw a lit patch on its dark page. The photographs' frame,
// --well and --photo, is light in both schemes by design.
func TestAppCSSColoursFollowTheScheme(t *testing.T) {
	t.Parallel()

	body, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	sheet := withoutBlocks(cssComment.ReplaceAllString(string(body), ""), lightDarkFallback)
	dayOnly := dayOnlyTokens(t)
	for _, frame := range []string{"--well", "--photo"} {
		if !dayOnly[frame] {
			t.Fatalf("%s is no longer a day-only token of %s; the frame's exception below needs another look", frame, BaseCSS)
		}
		delete(dayOnly, frame)
	}
	// A name app.css declares again on :root is the storefront's own from
	// there on, and its declaration is checked below like any other.
	for _, r := range cssRule.FindAllStringSubmatch(sheet, -1) {
		if strings.TrimSpace(r[1]) == ":root" {
			for _, d := range rootDecl.FindAllStringSubmatch(r[2], -1) {
				delete(dayOnly, d[1])
			}
		}
	}
	for _, must := range []string{"--n-0", "--n-900", "--ink", "--accent", "--error", "--line", "--wash", "--sh-lg"} {
		if !dayOnly[must] {
			t.Fatalf("dayOnlyTokens misses %s; the check would pass a sheet that draws it", must)
		}
	}
	// The day half of a light-dark() is what a pair is made of.
	dayHalf := regexp.MustCompile(`light-dark\(\s*(?:var\(--[a-z0-9-]+\)|#[0-9a-fA-F]{3,8})\s*,`)
	decl := regexp.MustCompile(`([a-z-]+)\s*:\s*([^;]+)`)
	used := make([]bool, len(schemeExceptions))
	for _, r := range cssRule.FindAllStringSubmatch(sheet, -1) {
		selectors := selectorList(r[1])
		for _, d := range decl.FindAllStringSubmatch(r[2], -1) {
			whole := strings.TrimSpace(d[0])
			value := dayHalf.ReplaceAllString(d[2], "light-dark(")
			found := dayOnlyColour.FindAllString(value, -1)
			for _, m := range customUse.FindAllStringSubmatch(value, -1) {
				if dayOnly[m[1]] {
					found = append(found, "var("+m[1]+")")
				}
			}
			if len(found) == 0 {
				continue
			}
			excused := false
			for i, e := range schemeExceptions {
				for _, sel := range selectors {
					if sel == e.selector && strings.Contains(whole, e.decl) {
						excused, used[i] = true, true
					}
				}
			}
			if !excused {
				t.Errorf("%s: %s { %s } draws %s, which has no night; use a role or a light-dark() pair",
					AppCSS, strings.Join(selectors, ", "), whole, strings.Join(found, ", "))
			}
		}
	}
	for i, e := range schemeExceptions {
		if !used[i] {
			t.Errorf("schemeExceptions names %s { … %s … }, which %s no longer declares; remove the row", e.selector, e.decl, AppCSS)
		}
	}

	// The night's arrow is the night's muted text written out, and has to stay it.
	night := strings.TrimPrefix(storefrontTokens(t)["--night-on-surface-variant"].night, "#")
	if night == "" || !strings.Contains(sheet, "stroke='%23"+night+"'") {
		t.Errorf("%s: the night's select arrow is not drawn in --night-on-surface-variant (#%s)", AppCSS, night)
	}
}

// appPairFallback finds the :root rule app.css gives a browser without
// light-dark().
var appPairFallback = regexp.MustCompile(`(?s)@supports not \(color: light-dark\(#000, #fff\)\) \{\s*:root \{(.*?)\}\s*\}`)

// TestAppCSSPairFallback checks that a browser without light-dark() draws each
// of app.css's light-dark() pairs by day. Such a browser finds the pair
// invalid where a rule reads it, and the rule falls back to its initial
// colour: black text, no ground, no edge. Each pair needs its day half in the
// fallback, and the fallback names nothing that is not a pair.
func TestAppCSSPairFallback(t *testing.T) {
	t.Parallel()

	body, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	app := cssComment.ReplaceAllString(string(body), "")
	block := appPairFallback.FindStringSubmatch(app)
	if block == nil {
		t.Fatalf("%s has no @supports not (color: light-dark(#000, #fff)) rule for :root", AppCSS)
	}
	fallback := make(map[string]string)
	for _, d := range rootDecl.FindAllStringSubmatch(block[1], -1) {
		fallback[d[1]] = strings.TrimSpace(d[2])
	}
	pair := regexp.MustCompile(`^light-dark\(\s*(var\(--[a-z0-9-]+\))\s*,`)
	pairs := make(map[string]string)
	for _, r := range cssRule.FindAllStringSubmatch(withoutBlocks(app, lightDarkFallback), -1) {
		if strings.TrimSpace(r[1]) != ":root" {
			continue
		}
		for _, d := range rootDecl.FindAllStringSubmatch(r[2], -1) {
			if m := pair.FindStringSubmatch(strings.TrimSpace(d[2])); m != nil {
				pairs[d[1]] = m[1]
			}
		}
	}
	if len(pairs) == 0 {
		t.Fatalf("%s declares no light-dark() pair on :root; the check found nothing to hold", AppCSS)
	}
	for name, day := range pairs {
		switch got, set := fallback[name]; {
		case !set:
			t.Errorf("without light-dark(), %s is unset, want %s, its day colour", name, day)
		case got != day:
			t.Errorf("without light-dark(), %s = %s, want %s, its day colour", name, got, day)
		}
	}
	for name := range fallback {
		if _, ok := pairs[name]; !ok {
			t.Errorf("the light-dark() fallback sets %s, which no light-dark() pair on :root declares", name)
		}
	}
}
