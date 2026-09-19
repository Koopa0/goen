package assets

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// fontURL matches a src: url(...) in the generated stylesheet, capturing the
// asset name and the digest the sheet claims for it.
var fontURL = regexp.MustCompile(`url\('` + regexp.QuoteMeta(Prefix) + `([^'?]+)\?v=([0-9a-f]+)'\)`)

// TestFontStylesheetMatchesTheEmbeddedFonts is what makes a generated file safe
// to check in.
//
// css/app/fonts.css is written by the one-time step in assets/fonts/README, and
// the digests in it are a copy of something this package computes. A copy goes
// stale: replace a subset and the sheet still names the old digest, the handler
// answers no-cache instead of immutable, and nothing else notices. This notices.
func TestFontStylesheetMatchesTheEmbeddedFonts(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, FontsCSS)
	if err != nil {
		t.Fatalf("read %s: %v", FontsCSS, err)
	}
	matches := fontURL.FindAllStringSubmatch(string(sheet), -1)
	if len(matches) == 0 {
		t.Fatalf("%s names no fonts; the regexp and the sheet disagree", FontsCSS)
	}

	named := make(map[string]bool, len(matches))
	for _, m := range matches {
		name, claimed := m[1], m[2]
		named[name] = true
		actual, embedded := catalogue.digests[name]
		if !embedded {
			t.Errorf("%s names %s, which is not embedded", FontsCSS, name)
			continue
		}
		if claimed != actual {
			t.Errorf("%s claims digest %s for %s; the embedded file hashes to %s — "+
				"regenerate the sheet (assets/fonts/README)", FontsCSS, claimed, name, actual)
		}
	}

	// And the other direction: a font nobody names is a font every visitor
	// carries in the binary for nothing.
	for name := range catalogue.digests {
		if path.Ext(name) == ".woff2" && !named[name] {
			t.Errorf("%s is embedded but %s names no rule for it", name, FontsCSS)
		}
	}
}

// TestNoStylesheetFetchesFromAnotherOrigin holds the promise style-src 'self'
// makes: nothing goen serves asks a browser to connect anywhere else.
//
// It is written to be independent of stripRemoteImports on purpose. A guard
// that reuses the rewrite's own matcher tests that the code agrees with itself,
// and the first version of this pair did exactly that — the matcher missed an
// @import split across two lines, and a test built on the same matcher would
// have missed it too. So this one strips comments itself, folds the whitespace
// itself, and then looks for any absolute URL in any @import or url() in the
// bytes goen actually sends.
func TestNoStylesheetFetchesFromAnotherOrigin(t *testing.T) {
	t.Parallel()

	var checked, rewritten int
	err := fs.WalkDir(files, "css", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(name) != ".css" {
			return err
		}
		checked++
		body, ok := catalogue.bodies[name]
		if ok {
			rewritten++
		} else {
			raw, readErr := fs.ReadFile(files, name)
			if readErr != nil {
				return readErr
			}
			body = raw
		}
		for _, target := range absoluteTargets(string(body)) {
			t.Errorf("%s asks the browser for %s; style-src is 'self' and it would "+
				"be refused, reported, and never arrive", name, target)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no stylesheets were checked; the walk found nothing")
	}
	// The design system's token sheet is the one that needs it. If a vendor
	// update drops the @import, this fails and the rewrite can go.
	if rewritten == 0 {
		t.Error("no stylesheet needed rewriting; stripRemoteImports is now dead code " +
			"and should be removed with the reason it existed")
	}
}

// absoluteTargets returns every URL in a stylesheet that names a scheme or a
// host, with comments removed and whitespace folded first so a statement
// written across lines reads the same as one written on one.
//
// It is deliberately blunt: it does not care whether the URL is in an @import,
// a src:, a background, or something CSS has not invented yet. Anything that
// names another origin in a sheet this site serves is a finding.
func absoluteTargets(css string) []string {
	// Comments out. Not "matched around" — out, so nothing inside one is read
	// as code and nothing outside one is hidden by one.
	var stripped strings.Builder
	for i := 0; i < len(css); {
		if strings.HasPrefix(css[i:], "/*") {
			end := strings.Index(css[i+2:], "*/")
			if end < 0 {
				break
			}
			i += end + 4
			stripped.WriteByte(' ')
			continue
		}
		stripped.WriteByte(css[i])
		i++
	}
	folded := strings.Join(strings.Fields(stripped.String()), " ")

	// A data: URL is a document carried in the sheet, not a place to fetch
	// from, and an inline SVG inside one carries an xmlns of http://www.w3.org/
	// — a namespace name no browser connects to. Cut each one out whole, to its
	// closing parenthesis rather than to its first quote, so what is inside one
	// is never read as a fetch. Cutting too much would only produce a finding
	// that is not there, which is the safe direction for a guard to err in.
	visible := inlineData.ReplaceAllString(folded, " ")

	found := absoluteURL.FindAllStringSubmatch(visible, -1)
	out := make([]string, 0, len(found))
	for _, m := range found {
		out = append(out, strings.Trim(m[0], `'"( `))
	}
	return out
}

// inlineData matches a url() holding a document rather than a location.
var inlineData = regexp.MustCompile(`(?is)url\(\s*['"]?(?:data|blob):[^)]*\)`)

// absoluteURL matches a scheme-and-authority or scheme-relative URL wherever it
// appears: https://host, //host, or any scheme followed by //.
var absoluteURL = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*:)?//[^\s'")]+`)

// TestEveryFontFamilyCarriesItsLicence holds the one obligation that travels
// with the files themselves.
//
// All three families are under the SIL Open Font License 1.1, which requires
// the licence and the copyright notice to accompany the font wherever it is
// redistributed — and goen redistributes them twice over, in the binary and
// over HTTP. A family added without its OFL.txt is a licence breach that
// nothing else in this repository would notice.
func TestEveryFontFamilyCarriesItsLicence(t *testing.T) {
	t.Parallel()

	families := map[string]bool{}
	err := fs.WalkDir(files, "fonts", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(name) != ".woff2" {
			return err
		}
		families[path.Dir(name)] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(families) == 0 {
		t.Fatal("no font families are embedded; the walk found nothing")
	}

	for dir := range families {
		licence, readErr := fs.ReadFile(files, path.Join(dir, "OFL.txt"))
		if readErr != nil {
			t.Errorf("%s has fonts but no OFL.txt: %v — the SIL Open Font License "+
				"requires it to travel with the files", dir, readErr)
			continue
		}
		text := string(licence)
		if !strings.Contains(text, "SIL OPEN FONT LICENSE Version 1.1") {
			t.Errorf("%s/OFL.txt does not carry the licence body", dir)
		}
		if !strings.HasPrefix(strings.TrimSpace(text), "Copyright") {
			t.Errorf("%s/OFL.txt does not open with a copyright notice", dir)
		}
	}
}
