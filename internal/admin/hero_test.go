package admin

import "testing"

// TestAHeroLinkMustBeAPathOnThisSite proves the largest button on the site
// cannot be pointed off it.
//
// A CTA href is typed by a person and rendered into the largest button on the
// storefront. An absolute URL there sends every visitor off-site from the home
// page, and "javascript:" puts script in it — so the field goes through
// web.SitePath, the same owner guarding the wishlist and the language switch.
//
// templ.SafeURL would neutralise a javascript: URL at render time. That is not
// a reason to accept one: a value that never lands cannot be rendered by some
// future template that forgets.
func TestAHeroLinkMustBeAPathOnThisSite(t *testing.T) {
	tests := []struct {
		name string
		href string
		ok   bool
	}{
		{"a page", "/deals", true},
		{"a page with a query", "/search?q=phone", true},
		{"the root", "/", true},
		{"absolute https", "https://evil.example", false},
		{"javascript", "javascript:alert(1)", false},
		{"protocol-relative", "//evil.example/x", false},
		{"backslash", `/\evil.example`, false},
		{"userinfo", "https://goen.example@evil.example/", false},
		{"empty", "", false},
		{"relative", "deals", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &HeroForm{Headline: "標題", PrimaryLabel: "去", PrimaryHref: tt.href}
			errs := f.Validate()
			_, refused := errs["primaryhref"]
			if refused == tt.ok {
				t.Errorf("href %q: refused=%v, want refused=%v (errors: %v)",
					tt.href, refused, !tt.ok, errs)
			}
		})
	}
}

// TestASecondaryButtonIsBothHalvesOrNeither proves half a button is refused
// with a message naming the missing half.
//
// hero_slides_secondary_cta_complete refuses half a button in the schema.
// Refusing it here is what turns a constraint name into a sentence naming the
// missing half.
func TestASecondaryButtonIsBothHalvesOrNeither(t *testing.T) {
	tests := []struct {
		name        string
		label, href string
		wantRefused bool
	}{
		{"neither", "", "", false},
		{"both", "關於", "/about", false},
		{"label only", "關於", "", true},
		{"href only", "", "/about", true},
		{"both, but the href is off-site", "關於", "https://evil.example", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &HeroForm{
				Headline: "標題", PrimaryLabel: "去", PrimaryHref: "/deals",
				SecondLabel: tt.label, SecondHref: tt.href,
			}
			if _, refused := f.Validate()["second"]; refused != tt.wantRefused {
				t.Errorf("refused=%v, want %v", refused, tt.wantRefused)
			}
		})
	}
}

// TestAnImageWithoutAltTextIsRefused proves a hero a screen reader cannot
// describe never reaches the table.
//
// hero_slides_image_has_alt says the same thing in the schema. Saying it here
// too is what names the field instead of showing a constraint — and alt text is
// the whole difference between a hero a screen reader can describe and one it
// cannot.
func TestAnImageWithoutAltTextIsRefused(t *testing.T) {
	const digest = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

	withImage := &HeroForm{
		Headline: "標題", PrimaryLabel: "去", PrimaryHref: "/deals", ImageKey: digest,
	}
	if _, refused := withImage.Validate()["alt"]; !refused {
		t.Error("an image with no alt text was accepted")
	}

	withAlt := &HeroForm{
		Headline: "標題", PrimaryLabel: "去", PrimaryHref: "/deals",
		ImageKey: digest, ImageAlt: "夏季展主視覺",
	}
	if errs := withAlt.Validate(); len(errs) > 0 {
		t.Errorf("a complete slide was refused: %v", errs)
	}

	// No image, no alt: fine. The built-in artwork carries alt="" on purpose,
	// because the headline beside it already says what it is.
	noImage := &HeroForm{Headline: "標題", PrimaryLabel: "去", PrimaryHref: "/deals"}
	if errs := noImage.Validate(); len(errs) > 0 {
		t.Errorf("a slide with no image was refused: %v", errs)
	}
}
