package site

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// TestTheStatedHoldMatchesTheEnforcedOne proves the page states the window the
// code enforces.
//
// The shipping page tells a customer their stock is held for N minutes.
// pages.HoldMinutes is that number and cart.HoldTTL is what actually holds it —
// two constants in two packages, because internal/ui must not import a feature
// package.
//
// Two constants that must agree and nothing making them is a promise that
// drifts. This is what makes them agree: change one and the build goes red
// rather than the page starting to lie.
func TestTheStatedHoldMatchesTheEnforcedOne(t *testing.T) {
	stated := pages.HoldMinutes
	enforced := int(cart.HoldTTL.Minutes())
	if stated != enforced {
		t.Errorf("the shipping page says stock is held for %d minutes and "+
			"cart.HoldTTL holds it for %d — the page is telling customers "+
			"something the till does not do", stated, enforced)
	}
}

// TestEveryPolicyRouteHasADocument proves every routed policy resolves, and
// every document is reachable.
//
// A route registered without an entry renders the 404 the footer links to,
// which is the state this whole change existed to remove. The list is the
// server's own — kept here rather than derived, because the routes live in
// package main and a test in this package cannot read them; the completeness
// this asserts is that each NAMED path resolves.
func TestEveryPolicyRouteHasADocument(t *testing.T) {
	// The paths cmd/goen registers to Policy.
	routed := []string{"returns", "payment", "warranty", "privacy", "terms"}
	for _, path := range routed {
		if _, ok := policies[path]; !ok {
			t.Errorf("/%s is routed to Policy and has no document; it would render "+
				"the 404 the footer links to", path)
		}
	}
	// And nothing unreachable: a document nobody routes to is prose nobody sees.
	for path := range policies {
		found := false
		for _, r := range routed {
			if r == path {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q has a document and no route", path)
		}
	}
}

// TestPolicyDocumentsAreComplete proves no policy renders as an empty page.
//
// A heading with no body, or a document with no sections, is a page that looks
// like a policy and says nothing — worse than an honest 404, because a reader
// concludes the shop has no rule rather than that the page is missing.
func TestPolicyDocumentsAreComplete(t *testing.T) {
	for path, doc := range policies {
		t.Run(path, func(t *testing.T) {
			if strings.TrimSpace(doc.Title) == "" {
				t.Error("no title")
			}
			if strings.TrimSpace(doc.Summary) == "" {
				t.Error("no summary; it fills the meta description")
			}
			if len(doc.Sections) == 0 {
				t.Fatal("no sections")
			}
			for i, s := range doc.Sections {
				if strings.TrimSpace(s.Heading) == "" {
					t.Errorf("section %d has no heading", i)
				}
				if len(s.Body) == 0 {
					t.Errorf("section %q has no body", s.Heading)
				}
				for j, para := range s.Body {
					if strings.TrimSpace(para) == "" {
						t.Errorf("section %q paragraph %d is blank", s.Heading, j)
					}
				}
			}
		})
	}
}

// TestUndecidedTermsAreMarkedPending proves a gap does not render as a rule.
//
// goen has commercial decisions it has not made — the return window, who pays
// return postage, the warranty term. Those paragraphs must be marked Pending so
// they render as a gap rather than a rule: a shop that sets both in the same
// typeface makes a promise by accident, and a customer holds it to one.
func TestUndecidedTermsAreMarkedPending(t *testing.T) {
	for path, doc := range policies {
		for _, s := range doc.Sections {
			for _, para := range s.Body {
				if strings.Contains(para, "尚未確定") && !s.Pending {
					t.Errorf("/%s: %q says something is undecided and is not marked "+
						"Pending, so it renders as a rule", path, s.Heading)
				}
			}
		}
	}
}

// TestTheShippingPageStatesTheSurchargeItCharges holds that the page says where
// costs extra, in the visitor's language.
//
// A customer in 金門 otherwise meets the number for the first time at the last
// step of the checkout. The page reads the figure from the same rows the till
// prices from, so the two cannot come to disagree — the reason the fee itself is
// read rather than written into the copy.
//
// The phrase around it used to be built by string_agg IN SQL, which made it
// Chinese for every reader: the words were right and there was nowhere in that
// query to ask who was reading. Asserting both locales is what would have caught
// it — HasSurcharges alone was green the whole time.
func TestTheShippingPageStatesTheSurchargeItCharges(t *testing.T) {
	t.Parallel()

	m := pages.ShippingMethod{
		Name: "宅配到府", FeeCents: 8000, FreeOverCents: 300000,
		Surcharges: []pages.ZoneSurcharge{
			{Name: "離島", Cents: 20000},
			{Name: "澎湖", Cents: 15000},
		},
	}
	if !m.HasSurcharges() {
		t.Error("a method with a surcharge reports none")
	}

	tests := []struct {
		name   string
		locale i18n.Locale
		want   string
	}{
		{
			name:   "Chinese enumerates with a 、",
			locale: i18n.ZhHant,
			want:   "離島 另加 NT$200、澎湖 另加 NT$150",
		},
		{
			// The zone NAMES stay as the shop typed them, and that is the line
			// CLAUDE.md draws: copy compiled into the binary is goen's to say in
			// both languages, copy typed into a table is the shop's to say however
			// it likes.
			name:   "English says it in English around the shop's own words",
			locale: i18n.En,
			want:   "離島 costs NT$200 extra, 澎湖 costs NT$150 extra",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := m.SurchargeText(i18n.WithLocale(t.Context(), tt.locale))
			if got != tt.want {
				t.Errorf("SurchargeText(%s) = %q, want %q", tt.locale, got, tt.want)
			}
		})
	}

	plain := pages.ShippingMethod{Name: "超商取貨", FeeCents: 6000}
	if plain.HasSurcharges() {
		t.Error("a method with no surcharge reports one")
	}
	if plain.SurchargeText(i18n.WithLocale(t.Context(), i18n.En)) != "" {
		t.Error("a method with no surcharge still says something")
	}
}
