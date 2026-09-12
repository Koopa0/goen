package site

import (
	"errors"
	"fmt"
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
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ui/pages"
)

// TestTheStatedHoldMatchesTheEnforcedOne proves every customer-facing policy
// states the one presentation-layer hold duration.
func TestTheStatedHoldMatchesTheEnforcedOne(t *testing.T) {
	enforced := pages.HoldMinutesText()

	// The FAQ makes the same promise, from a seeded row.
	seed, err := os.ReadFile(filepath.Join("..", "..", "seed", "dev_catalog.sql"))
	if err != nil {
		t.Fatalf("read the seed: %v", err)
	}
	for _, want := range []string{
		fmt.Sprintf("保留庫存 %s 分鐘", enforced),
		fmt.Sprintf("holds the stock for %s minutes", enforced),
	} {
		if !strings.Contains(string(seed), want) {
			t.Errorf("no FAQ row states %q, so the answer a customer reads and the "+
				"window the sweeper enforces are two different numbers", want)
		}
	}
}

// TestActiveInvoiceFAQDoesNotCallTheIssuerUnbuilt holds /faq to the issuer
// that is already wired. A missing merchant id is a deployment, not an
// unfinished product; saying 「尚未完成」 while Gateway.Issue exists is two
// answers to one customer question.
func TestActiveInvoiceFAQDoesNotCallTheIssuerUnbuilt(t *testing.T) {
	t.Parallel()

	g, err := invoice.NewGateway("", "", "", "")
	if err != nil {
		t.Fatalf("an empty configuration must be legal: %v", err)
	}
	if _, issueErr := g.Issue(t.Context(), invoice.IssueRequest{}); !errors.Is(issueErr, invoice.ErrDisabled) {
		t.Fatalf("Gateway.Issue is not wired: %v", issueErr)
	}

	seed, err := os.ReadFile(filepath.Join("..", "..", "seed", "dev_catalog.sql"))
	if err != nil {
		t.Fatalf("read the seed: %v", err)
	}
	repair, err := os.ReadFile(filepath.Join("..", "..", "seed", "repair_invoice_faq.sql"))
	if err != nil {
		t.Fatalf("read the invoice FAQ repair: %v", err)
	}
	src := string(seed)
	fix := string(repair)

	zhInsert := sqlStringAfter(t, src, "('發票', '發票怎麼開立?',")
	enValues := sqlStringAfter(t, src, "'How is my invoice issued?',")
	rewrite := rewriteInvoiceFAQ(t, fix)
	if zhInsert != rewrite.zh || enValues != rewrite.en {
		t.Errorf("fresh INSERT/VALUES and the shipped repair disagree:\n"+
			"  insert zh = %q\n  repair zh = %q\n  values en = %q\n  repair en = %q",
			zhInsert, rewrite.zh, enValues, rewrite.en)
	}
	if strings.Contains(fix, "INSERT INTO") || strings.Contains(fix, "DELETE FROM") ||
		strings.Contains(fix, "UPDATE brands") || strings.Contains(fix, "UPDATE products") {
		t.Error("the invoice FAQ repair is not bounded: it must rewrite one FAQ row")
	}

	for loc, answer := range map[string]string{"zh-Hant": rewrite.zh, "en": rewrite.en} {
		if strings.Contains(answer, "尚未完成") || strings.Contains(answer, "not built yet") {
			t.Errorf("%s invoice FAQ still says the issuer is unfinished: %q", loc, answer)
		}
		if strings.Contains(answer, "successfully issued") ||
			strings.Contains(answer, "已成功開立") {
			t.Errorf("%s invoice FAQ invents a successful filing: %q", loc, answer)
		}
	}
	if !strings.Contains(rewrite.zh, "綠界") || !strings.Contains(rewrite.zh, "設定") {
		t.Errorf("Chinese invoice FAQ does not describe ECPay as a deployment: %q", rewrite.zh)
	}
	if !strings.Contains(rewrite.en, "ECPay") || !strings.Contains(rewrite.en, "credentials") {
		t.Errorf("English invoice FAQ does not describe ECPay as a deployment: %q", rewrite.en)
	}

	// The statutory return row is a different authority. Editing it here
	// would reopen 消保法 §19.
	if !strings.Contains(src, "退貨運費由 goen 負擔") ||
		!strings.Contains(src, "Rescinding within seven days of delivery costs you nothing") {
		t.Error("the statutory return FAQ was edited")
	}
}

type invoiceFAQCopy struct{ zh, en string }

func rewriteInvoiceFAQ(t *testing.T, src string) invoiceFAQCopy {
	t.Helper()
	const where = "WHERE question = '發票怎麼開立?';"
	i := strings.LastIndex(src, where)
	if i < 0 {
		t.Fatal("the shipped repair has no UPDATE of 發票怎麼開立?")
	}
	block := src[:i]
	start := strings.LastIndex(block, "UPDATE faq_entries")
	if start < 0 {
		t.Fatal("the shipped repair has no UPDATE faq_entries")
	}
	block = block[start:]
	return invoiceFAQCopy{
		zh: sqlStringAfter(t, block, "SET answer = "),
		en: sqlStringAfter(t, block, "answer_en = "),
	}
}

func sqlStringAfter(t *testing.T, src, marker string) string {
	t.Helper()
	i := strings.Index(src, marker)
	if i < 0 {
		t.Fatalf("seed has no %q", marker)
	}
	rest := strings.TrimLeft(src[i+len(marker):], " \t\n")
	if !strings.HasPrefix(rest, "'") {
		t.Fatalf("seed text after %q is not a SQL string: %q", marker, rest[:min(40, len(rest))])
	}
	var b strings.Builder
	for j := 1; j < len(rest); j++ {
		if rest[j] == '\'' {
			if j+1 < len(rest) && rest[j+1] == '\'' {
				b.WriteByte('\'')
				j++
				continue
			}
			return b.String()
		}
		b.WriteByte(rest[j])
	}
	t.Fatalf("unterminated SQL string after %q", marker)
	return ""
}

// TestEveryPolicyRouteHasADocument proves every routed policy resolves, and
// every document is reachable.
func TestEveryPolicyRouteHasADocument(t *testing.T) {
	// The paths cmd/goen registers to Policy, which a test here cannot read.
	routed := []string{"returns", "payment", "warranty", "privacy", "terms"}
	for _, path := range routed {
		if _, ok := policies[path]; !ok {
			t.Errorf("/%s is routed to Policy and has no document; it would render "+
				"the 404 the footer links to", path)
		}
	}
	for path := range policies {
		found := slices.Contains(routed, path)
		if !found {
			t.Errorf("%q has a document and no route", path)
		}
	}
}

// TestThePrivacyPolicyNamesEveryCookie holds a sentence that CLAIMS
// completeness against the cookies the binary actually sets.
func TestThePrivacyPolicyNamesEveryCookie(t *testing.T) {
	described := map[string]struct{ zh, en string }{
		cart.CookieName:           {zh: "購物車", en: "your cart"},
		account.SessionCookieName: {zh: "登入狀態", en: "your sign-in"},
		cart.PlacedCookieName:     {zh: "訂單瀏覽權限", en: "permission to view an order"},
		i18n.CookieName:           {zh: "您選擇的語言", en: "the language you chose"},
		home.DismissCookie:        {zh: "您關閉過的網站公告", en: "which site notice you have dismissed"},
		"__Host-goen_oauth":       {zh: "用 Google 登入時暫存", en: "while you sign in with Google"},
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

	for _, name := range hostCookieNames(t) {
		if _, ok := described[name]; !ok {
			t.Errorf("%s is set by the binary and the privacy policy does not "+
				"account for it; the page tells every visitor it sets no others", name)
		}
	}
}

// hostCookieNames is every __Host- cookie name declared anywhere in internal/.
// The bare names beside them are the same cookie under GOEN_INSECURE_COOKIES.
func hostCookieNames(t *testing.T) []string {
	t.Helper()
	pattern := regexp.MustCompile(`"(__Host-goen_[a-z_]+)"`)
	seen := map[string]bool{}
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Tests excluded: a cookie a test names is not one the shop sets.
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
func TestUndecidedTermsAreMarkedPending(t *testing.T) {
	for path, doc := range policies {
		for _, s := range doc.Sections {
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

// TestEveryPolicyClauseIsTranslated refuses a policy page that goes
// half-English: /returns states Consumer Protection Act §19, which §18 I 3
// obliges the trader to provide whatever language the customer reads.
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

// TestStatutoryTermsAreNotPending proves a RIGHT does not render as a gap. The
// assertion is POSITIVE because legitimate Pending copy must name the statutory
// window, so a forbidden-word scan would refuse the correct text.
func TestStatutoryTermsAreNotPending(t *testing.T) {
	// Each entry is a term Taiwanese law fixes, its citation, and the substance
	// the page must state outside a Pending section — in both languages, because
	// a right stated in one is stated for one reader.
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
			// The zone NAMES stay as the shop typed them.
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
