package i18n

import (
	"strings"
	"testing"
)

// namedSlogans are the phrases issue #268 listed as generic storefront
// marketing. A replacement slogan that reuses one still fails the customer.
var namedSlogans = []string{
	"挑一台好的",
	"A good one is worth choosing",
	"把難挑的東西",
	"the hard decisions made for you",
	"讓買家與對的好商品相遇",
	"規格看得懂",
	"保固靠得住",
	"買 3C 不該是運氣",
	"精選不灌水",
	"新品與比價重點,不灌水",
	"chosen rather than listed",
	"right things",
	"warranty holds",
	"nothing padded",
	"curated 3C",
	"精選 3C",
	"Chosen, not padded",
	"你只要選",
	"you only have to choose",
}

func TestNamedStorefrontSlogansAreGone(t *testing.T) {
	t.Parallel()

	if len(messages) == 0 {
		t.Fatal("the catalogue is empty; this test would pass on nothing")
	}

	for _, slogan := range namedSlogans {
		for k, m := range messages {
			for _, l := range Locales() {
				if strings.Contains(m.in(l), slogan) {
					t.Errorf("%s (%s) still carries %q: %q", k, l, slogan, m.in(l))
				}
			}
		}
	}
}

// brandCopyJobs are the surfaces that must not share one positioning sentence.
var brandCopyJobs = []struct {
	name string
	key  Key
}{
	{name: "home title", key: KeyHomeTitle},
	{name: "home hero headline", key: KeyHeroHeadline},
	{name: "site title", key: KeySiteTitle},
	{name: "about heading", key: KeyTagline},
	{name: "footer tagline", key: KeyFooterTagline},
	{name: "about description", key: KeyAboutDescription},
	{name: "newsletter note", key: KeyNewsletterNote},
	{name: "listing description", key: KeyListingDescription},
}

func TestBrandCopyJobsDoNotShareASentence(t *testing.T) {
	t.Parallel()

	for _, l := range Locales() {
		seen := map[string]string{}
		for _, job := range brandCopyJobs {
			got := messages[job.key].in(l)
			if strings.TrimSpace(got) == "" {
				t.Errorf("%s (%s) is empty; a job still needs a sentence", job.name, l)
				continue
			}
			if other, dup := seen[got]; dup {
				t.Errorf("%s and %s share %q in %s", job.name, other, got, l)
			}
			seen[got] = job.name
		}
	}
}

func TestBuiltInHeroNamesTheCatalogue(t *testing.T) {
	t.Parallel()

	zh := messages[KeyHeroHeadline].ZhHant
	en := messages[KeyHeroHeadline].En
	for _, word := range []string{"手機", "筆電", "平板", "耳機"} {
		if !strings.Contains(zh, word) {
			t.Errorf("Chinese hero headline %q does not name %s", zh, word)
		}
	}
	for _, word := range []string{"Phones", "laptops", "tablets", "headphones"} {
		if !strings.Contains(en, word) {
			t.Errorf("English hero headline %q does not name %s", en, word)
		}
	}
}
