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
// goen has commercial decisions it has not made — the warranty term, a goodwill
// return beyond the statutory window. Those paragraphs must be marked Pending so
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

// TestEveryPolicyClauseIsTranslated refuses a policy page that goes half-English.
//
// These documents were Chinese for every visitor, exempted from
// TestNoChromeStringIsHardCoded as "authored prose, translated editorially or
// not at all". That put them on the CONTENT side of the line this project
// draws, and CLAUDE.md's own test says otherwise: copy compiled into the binary
// is goen's to say in both languages; copy typed into a table is the shop's to
// say however it likes. `faq_entries` is the table. These are compiled in.
//
// It stopped being untidy and became a legal exposure when /returns began
// stating 消保法 §19 — §18 I 3 makes providing the rescission information the
// trader's obligation, and an English-reading customer in Taiwan holds the same
// unwaivable right on a page they could not read.
//
// The failure this locks is the one the i18n work already met once: a half-
// translated document reads as a broken page rather than as untranslated
// content, and only to the visitor.
func TestEveryPolicyClauseIsTranslated(t *testing.T) {
	t.Parallel()

	han := func(s string) bool {
		for _, r := range s {
			if r >= 0x4E00 && r <= 0x9FFF {
				return true
			}
		}
		return false
	}

	for path, doc := range policies {
		en := doc.For(i18n.En)
		if en.Title == "" || en.Summary == "" {
			t.Errorf("/%s has no English title or summary", path)
		}
		if han(en.Title) || han(en.Summary) {
			t.Errorf("/%s: the English title or summary still carries Han — an "+
				"entry that LOOKS filled in is the copy-paste this catches", path)
		}
		if len(en.Sections) != len(doc.Sections) {
			t.Fatalf("/%s: %d English sections against %d Chinese",
				path, len(en.Sections), len(doc.Sections))
		}
		for i, s := range en.Sections {
			zh := doc.Sections[i]
			if s.Heading == "" {
				t.Errorf("/%s: section %q has no English heading", path, zh.Heading)
			}
			if len(s.Body) != len(zh.Body) {
				t.Errorf("/%s: section %q has %d English paragraphs against %d Chinese "+
					"— a clause the English reader simply does not get",
					path, zh.Heading, len(s.Body), len(zh.Body))
			}
			for _, para := range append([]string{s.Heading}, s.Body...) {
				if para == "" {
					t.Errorf("/%s: section %q has an empty English paragraph", path, zh.Heading)
					continue
				}
				if han(para) {
					t.Errorf("/%s: section %q still reads Chinese in English: %q",
						path, zh.Heading, para)
				}
			}
		}
	}
}

// TestStatutoryTermsAreNotPending proves a RIGHT does not render as a gap.
//
// It is the mirror of the test above, and it exists because the mirror SHIPPED.
// 鑑賞期天數, who pays return postage, and whether opening the box matters were
// all filed under 尚未確定 — and all three are fixed by 消保法 §19, which §19 V
// makes unwaivable. They were never the shop's to decide, so marking them
// undecided told every customer they might have no right at all.
//
// Neither the test above nor any other guard in this repository could see it. A
// Pending section is FORMATTED as a gap, so it looks correct to a reviewer who
// has not read §19, and TestUndecidedTermsAreMarkedPending passes on it by
// construction — the paragraph said 尚未確定 and was marked Pending, which is
// exactly what that test asks for.
//
// The assertion is POSITIVE — each term must be stated as a rule somewhere the
// reader does not see a gap — rather than a scan of Pending sections for
// forbidden words. The legitimate Pending copy has to NAME the statutory window
// in order to say what lies outside it, so a forbidden-word scan would refuse
// the correct text and pass the wrong text the moment somebody paraphrased.
//
// The wanted strings are literal on purpose. Rewording a statutory clause should
// send whoever did it back to the citation to confirm the law still says that,
// which is the same reason a wire-format test does not read the code's own
// constant.
func TestStatutoryTermsAreNotPending(t *testing.T) {
	// Each entry is a term Taiwanese law fixes, the citation a future editor
	// needs in order to DISAGREE with the entry rather than quietly delete it,
	// and the substance the page must state outside a Pending section.
	statutory := []struct {
		term string
		cite string
		doc  string
		want []string
	}{
		{
			term: "the length of the rescission window",
			cite: "消保法 §19 I — seven days from receipt of the goods; §19 V voids any agreement otherwise; 民法 §120 II excludes the day of receipt",
			doc:  "returns",
			want: []string{"七天的鑑賞期", "「隔天」開始算"},
		},
		{
			term: "who pays return postage",
			cite: "消保法 §19 I — the consumer bears 任何費用, which is to say none",
			doc:  "returns",
			want: []string{"退貨運費由 goen 負擔"},
		},
		{
			term: "whether opening the box forfeits the right",
			cite: "通訊交易解除權合理例外情事適用準則 §2 — a closed list of seven, and opened 3C hardware is on none of them",
			doc:  "returns",
			want: []string{"拆封後仍在鑑賞期內"},
		},
	}

	for _, s := range statutory {
		t.Run(s.term, func(t *testing.T) {
			doc, ok := policies[s.doc]
			if !ok {
				t.Fatalf("no policy document at /%s", s.doc)
			}
			// Only sections that render as a RULE count. A statement of a
			// statutory term inside a Pending section is the defect.
			var stated strings.Builder
			for _, sec := range doc.Sections {
				if sec.Pending {
					continue
				}
				for _, para := range sec.Body {
					stated.WriteString(para)
					stated.WriteString("\n")
				}
			}
			for _, w := range s.want {
				if !strings.Contains(stated.String(), w) {
					t.Errorf("/%s does not state %s outside a Pending section: no %q.\n"+
						"This is not the shop's to leave undecided — %s",
						s.doc, s.term, w, s.cite)
				}
			}
		})
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
