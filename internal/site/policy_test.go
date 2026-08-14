package site

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/home"
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
// which is exactly the state this refuses. The list is the
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

// TestThePrivacyPolicyNamesEveryCookie holds a sentence that CLAIMS
// completeness against the cookies the binary actually sets.
//
// 「goen 使用的 cookie 只有…」 / "and no others" is a falsifiable statement, and
// naming fewer kinds than the site sets is reachable by an ordinary visitor: the
// language cookie is written by the switch in the footer of EVERY page, so
// anybody who has changed language is carrying an undisclosed one, and the
// promotional-strip dismissal is another.
//
// This is the shape TestTheStatedHoldMatchesTheEnforcedOne has one section over:
// a page-says-versus-code-does guard. Each such sentence needs its own, because
// one guard does not extend itself to the next page that states a number, a list
// or a limit the code also knows — which is how a sentence drifts with nothing
// going red.
//
// Derived by WALKING THE SOURCE rather than from a list, so a sixth cookie fails
// this the moment its constant is declared. A hand-written list needs somebody
// to remember to extend it, which is the failure this exists to catch.
func TestThePrivacyPolicyNamesEveryCookie(t *testing.T) {
	// One entry per cookie the binary can set, naming the words the policy uses
	// for it. Both locales, because BodyEn is the half no other guard reads.
	described := map[string]struct{ zh, en string }{
		cart.CookieName:           {zh: "購物車", en: "your cart"},
		account.SessionCookieName: {zh: "登入狀態", en: "your sign-in"},
		cart.PlacedCookieName:     {zh: "訂單瀏覽權限", en: "permission to view an order"},
		i18n.CookieName:           {zh: "您選擇的語言", en: "the language you chose"},
		home.DismissCookie:        {zh: "您關閉過的網站公告", en: "which site notice you have dismissed"},
		// Set only while a Google sign-in is in flight, and cleared by the
		// callback whatever the outcome. Disclosed anyway: the clause claims
		// completeness, and "it only lasts ten minutes" is not an exemption from
		// a sentence that says "and no others".
		"__Host-goen_oauth": {zh: "用 Google 登入時暫存", en: "while you sign in with Google"},
	}

	var zh, en strings.Builder
	for _, s := range policies["privacy"].Sections {
		for _, p := range s.Body {
			zh.WriteString(p)
		}
		for _, p := range s.BodyEn {
			en.WriteString(p)
		}
	}

	for name, words := range described {
		if !strings.Contains(zh.String(), words.zh) {
			t.Errorf("the privacy policy claims to list every cookie and does not "+
				"mention %s (%q)", name, words.zh)
		}
		if !strings.Contains(en.String(), words.en) {
			t.Errorf("the English privacy policy does not mention %s (%q)", name, words.en)
		}
	}

	// Completeness: every `__Host-goen_*` literal in the tree has an entry above.
	// Without this the map is a list somebody has to remember to extend, which is
	// the failure mode this test exists for.
	for _, name := range hostCookieNames(t) {
		if _, ok := described[name]; !ok {
			t.Errorf("%s is set by the binary and the privacy policy does not "+
				"account for it; the page tells every visitor it sets no others", name)
		}
	}
}

// hostCookieNames is every __Host- cookie name declared anywhere in internal/.
//
// The __Host- prefix is what makes this findable: goen's cookies all carry it
// (the bare names beside them are the development variants of the same cookie,
// gated on GOEN_INSECURE_COOKIES), so one pattern reaches all of them.
func hostCookieNames(t *testing.T) []string {
	t.Helper()
	pattern := regexp.MustCompile(`"(__Host-goen_[a-z_]+)"`)
	seen := map[string]bool{}
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Tests excluded: a cookie a test names is not a cookie the shop sets,
		// and this file itself names all five.
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path) //nolint:gosec // G304: paths come from walking this repository
		if readErr != nil {
			return readErr
		}
		for _, m := range pattern.FindAllStringSubmatch(string(src), -1) {
			seen[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
	if len(seen) == 0 {
		t.Fatal("found no cookie names at all; the sweep is not reading the source")
	}
	return slices.Sorted(maps.Keys(seen))
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
			// BOTH halves. Reading s.Body alone lets the English say a term is
			// undecided outside a Pending section with nothing ever looking, and
			// the two halves really do come apart: a Pending paragraph listing
			// 保固期限 in Chinese and not in English, above a section stating that
			// same term as a rule. A guard over one locale is a guard over the
			// locale whoever wrote it happened to read.
			for _, para := range append(append([]string{}, s.Body...), s.BodyEn...) {
				if (strings.Contains(para, "尚未確定") ||
					strings.Contains(para, "not decided") ||
					strings.Contains(para, "not yet decided")) && !s.Pending {
					t.Errorf("/%s: %q says something is undecided and is not marked "+
						"Pending, so it renders as a rule", path, s.Heading)
				}
			}
		}
	}
}

// TestEveryPolicyClauseIsTranslated refuses a policy page that goes half-English.
//
// Leaving these Chinese for every visitor would put them on the CONTENT side of
// the line this project draws — the ground TestNoChromeStringIsHardCoded exempts
// as "authored prose, translated editorially or not at all" — and the line runs
// the other way: copy compiled into the binary is goen's to say in both
// languages; copy typed into a table is the shop's to say however it likes.
// `faq_entries` is the table. These are compiled in.
//
// It is a legal exposure rather than untidiness, because /returns states
// 消保法 §19 — §18 I 3 makes providing the rescission information the trader's
// obligation, and an English-reading customer in Taiwan holds the same
// unwaivable right on a page they cannot read.
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
// It is the mirror of the test above, and the mirror is the expensive
// direction. 鑑賞期天數, who pays return postage, and whether opening the box
// matters are all fixed by 消保法 §19, which §19 V makes unwaivable — never the
// shop's to decide — so filing any of them under 尚未確定 tells every customer
// they might have no right at all.
//
// Neither the test above nor any other guard in this repository can see that. A
// Pending section is FORMATTED as a gap, so it looks correct to a reviewer who
// has not read §19, and TestUndecidedTermsAreMarkedPending passes on it BY
// CONSTRUCTION: the paragraph says 尚未確定 and is marked Pending, which is
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
	// wantEn as well as want, because an unwaivable right stated in one language
	// is stated for one reader. Asserting the Chinese alone lets the English half
	// of every term below be dropped, softened or quietly turned into a shop
	// policy with nothing going red — and the neighbouring Pending guard has the
	// same blind spot, which is where the two halves really do come apart.
	statutory := []struct {
		term   string
		cite   string
		doc    string
		want   []string
		wantEn []string
	}{
		{
			term:   "the length of the rescission window",
			cite:   "消保法 §19 I — seven days from receipt of the goods; §19 V voids any agreement otherwise; 民法 §120 II excludes the day of receipt",
			doc:    "returns",
			want:   []string{"七天的鑑賞期", "「隔天」開始算"},
			wantEn: []string{"seven days to cancel", "the day AFTER"},
		},
		{
			term:   "who pays return postage",
			cite:   "消保法 §19 I — the consumer bears 任何費用, which is to say none",
			doc:    "returns",
			want:   []string{"退貨運費由 goen 負擔"},
			wantEn: []string{"return postage included"},
		},
		{
			term:   "whether opening the box forfeits the right",
			cite:   "通訊交易解除權合理例外情事適用準則 §2 — a closed list of seven, and opened 3C hardware is on none of them",
			doc:    "returns",
			want:   []string{"拆封後仍在鑑賞期內"},
			wantEn: []string{"opening 3C hardware keeps you inside the seven days"},
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
			var stated, statedEn strings.Builder
			for _, sec := range doc.Sections {
				if sec.Pending {
					continue
				}
				for _, para := range sec.Body {
					stated.WriteString(para)
					stated.WriteString("\n")
				}
				for _, para := range sec.BodyEn {
					statedEn.WriteString(para)
					statedEn.WriteString("\n")
				}
			}
			for _, w := range s.want {
				if !strings.Contains(stated.String(), w) {
					t.Errorf("/%s does not state %s outside a Pending section: no %q.\n"+
						"This is not the shop's to leave undecided — %s",
						s.doc, s.term, w, s.cite)
				}
			}
			for _, w := range s.wantEn {
				if !strings.Contains(statedEn.String(), w) {
					t.Errorf("/%s does not state %s to an ENGLISH reader outside a "+
						"Pending section: no %q.\nThe right does not depend on which "+
						"language the customer reads — %s", s.doc, s.term, w, s.cite)
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
// BOTH locales are asserted, and that is the half worth having. Build the phrase
// around the figure with a string_agg in SQL and it is Chinese for every reader —
// the words are right and there is nowhere in that query to ask who is reading —
// while HasSurcharges stays green throughout, because a surcharge that exists is
// a different question from a surcharge stated in the reader's language.
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
