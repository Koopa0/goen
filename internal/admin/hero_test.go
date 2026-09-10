package admin

import "testing"

// TestAHeroLinkMustBeAPathOnThisSite proves that button cannot be pointed off-site.
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
			errs := f.Validate(t.Context())
			_, refused := errs["primaryhref"]
			if refused == tt.ok {
				t.Errorf("href %q: refused=%v, want refused=%v (errors: %v)",
					tt.href, refused, !tt.ok, errs)
			}
		})
	}
}

// TestASecondaryButtonIsBothHalvesOrNeither proves half a button is refused.
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
			if _, refused := f.Validate(t.Context())["second"]; refused != tt.wantRefused {
				t.Errorf("refused=%v, want %v", refused, tt.wantRefused)
			}
		})
	}
}

// TestAnImageWithoutAltTextIsRefused proves an undescribable hero is refused.
func TestAnImageWithoutAltTextIsRefused(t *testing.T) {
	const digest = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

	withImage := &HeroForm{
		Headline: "標題", PrimaryLabel: "去", PrimaryHref: "/deals", ImageKey: digest,
	}
	if _, refused := withImage.Validate(t.Context())["alt"]; !refused {
		t.Error("an image with no alt text was accepted")
	}

	withAlt := &HeroForm{
		Headline: "標題", PrimaryLabel: "去", PrimaryHref: "/deals",
		ImageKey: digest, ImageAlt: "夏季展主視覺",
	}
	if errs := withAlt.Validate(t.Context()); len(errs) > 0 {
		t.Errorf("a complete slide was refused: %v", errs)
	}

	noImage := &HeroForm{Headline: "標題", PrimaryLabel: "去", PrimaryHref: "/deals"}
	if errs := noImage.Validate(t.Context()); len(errs) > 0 {
		t.Errorf("a slide with no image was refused: %v", errs)
	}
}
