package site

import (
	"errors"
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
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestHomeRefundTimingDoesNotOutrunReturns holds the home trust strip to the
// /returns refund clause: a numbered landing window on the home is a second
// SLA, and the issuer decides.
func TestHomeRefundTimingDoesNotOutrunReturns(t *testing.T) {
	t.Parallel()

	doc, ok := policies["returns"]
	if !ok {
		t.Fatal("no /returns document; the home strip has nothing to agree with")
	}

	var policyZH, policyEN strings.Builder
	for _, s := range doc.Sections {
		if s.Heading != "退款" {
			continue
		}
		for _, p := range s.Body {
			policyZH.WriteString(p)
		}
		for _, p := range s.BodyEn {
			policyEN.WriteString(p)
		}
	}
	if policyZH.Len() == 0 || policyEN.Len() == 0 {
		t.Fatal("the returns refund section is empty in one locale")
	}

	homeNotes := func(locale i18n.Locale) string {
		ctx := i18n.WithLocale(t.Context(), locale)
		return i18n.T(ctx, i18n.KeyRuleRescissionNote) + " " + i18n.T(ctx, i18n.KeyRuleReturnNote)
	}
	homeZH, homeEN := homeNotes(i18n.ZhHant), homeNotes(i18n.En)

	for _, tt := range []struct {
		locale, home, policy string
	}{
		{"zh-Hant", homeZH, policyZH.String()},
		{"en", homeEN, policyEN.String()},
	} {
		for _, needle := range []string{"3–5", "3-5", "3—5", "3 to 5", "3～5", "3~5"} {
			if strings.Contains(tt.home, needle) && !strings.Contains(tt.policy, needle) {
				t.Errorf("home shop rules %s promise %q and /returns does not:\n  home: %q\n  /returns: %q",
					tt.locale, needle, tt.home, tt.policy)
			}
		}
	}
}

// TestRefundCopyNamesBothPayoutChannels holds the three surfaces that used to
// describe every refund as Stripe. compensate_return_with_credit pays the
// store-credit half, so those sentences cannot stay Stripe-only.
func TestRefundCopyNamesBothPayoutChannels(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "migrations", "001_initial_schema.up.sql"))
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	if !strings.Contains(string(schema), "CREATE FUNCTION compensate_return_with_credit") {
		t.Fatal("compensate_return_with_credit is gone; this lock describes a payout that no longer exists")
	}

	oldStripeOnly := []string{
		"系統會立即向 Stripe 發出退款",
		"立刻透過 Stripe 退款",
		"we ask Stripe to refund",
		"refunds it through Stripe immediately",
		"A refund always goes back to the way you paid. We will not substitute another channel.",
	}
	mustNameBoth := func(t *testing.T, surface, text string) {
		t.Helper()
		hasCredit := strings.Contains(text, "購物金") ||
			strings.Contains(strings.ToLower(text), "store credit")
		if !hasCredit {
			t.Errorf("%s does not name the store-credit refund channel:\n%s", surface, text)
		}
		for _, old := range oldStripeOnly {
			if strings.Contains(text, old) {
				t.Errorf("%s still claims every refund goes through Stripe (%q)", surface, old)
			}
		}
	}

	doc := policies["returns"]
	var refund, refundEn strings.Builder
	for _, s := range doc.Sections {
		if s.Heading != "退款" {
			continue
		}
		for _, p := range s.Body {
			refund.WriteString(p)
			refund.WriteString("\n")
		}
		for _, p := range s.BodyEn {
			refundEn.WriteString(p)
			refundEn.WriteString("\n")
		}
	}
	if refund.Len() == 0 || refundEn.Len() == 0 {
		t.Fatal("/returns has no refund section")
	}
	mustNameBoth(t, "/returns 退款 (zh-Hant)", refund.String())
	mustNameBoth(t, "/returns Refunds (en)", refundEn.String())

	seed, err := os.ReadFile(filepath.Join("..", "..", "seed", "dev_catalog.sql"))
	if err != nil {
		t.Fatalf("read the seed: %v", err)
	}
	src := string(seed)
	const faqQuestion = "退款什麼時候會收到？"
	insertAt := strings.Index(src, "('退貨', '"+faqQuestion+"'")
	if insertAt < 0 {
		t.Fatal("seed has no refund FAQ INSERT")
	}
	insert := src[insertAt:]
	if end := strings.Index(insert, ", 20)"); end > 0 {
		insert = insert[:end]
	}
	mustNameBoth(t, "seed FAQ INSERT (zh-Hant)", insert)

	enAt := strings.Index(src, "('"+faqQuestion+"', 'When will I get my refund?'")
	if enAt < 0 {
		t.Fatal("seed has no refund FAQ English")
	}
	english := src[enAt:]
	if end := strings.Index(english, "'),"); end > 0 {
		english = english[:end]
	}
	mustNameBoth(t, "seed FAQ English (en)", english)

	const oldZh = "退貨經審核同意後,系統會立即向 Stripe 發出退款。實際入帳時間依發卡銀行而定,通常是數個工作天。"
	const oldEn = "As soon as a return is approved we ask Stripe to refund. When it lands depends on your card issuer, usually a few working days."
	if strings.Contains(src, oldZh) || strings.Contains(src, oldEn) {
		t.Error("catalogue seed still carries the Stripe-only refund sentences; " +
			"a kept database never reaches an UPDATE buried after brands_pkey")
	}

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		lead := i18n.T(i18n.WithLocale(t.Context(), locale), i18n.KeyAdminRetLead)
		mustNameBoth(t, "KeyAdminRetLead ("+string(locale)+")", lead)
	}
}

// TestNoPolicyPromisesALapsedOrderCanBePaid holds /payment and /shipping to
// what the hold sweeper does: an order still unpaid when its hold ends is
// cancelled, and nothing re-reserves its stock.
func TestNoPolicyPromisesALapsedOrderCanBePaid(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		parts := []string{i18n.T(i18n.WithLocale(t.Context(), locale), i18n.KeyShippingHoldBody)}
		for _, section := range policies["payment"].For(locale, testWindows).Sections {
			parts = append(parts, section.Body...)
		}
		body := strings.Join(parts, " ")
		cancelled, retry := "自動取消", "重新付款"
		if locale == i18n.En {
			cancelled, retry = "cancelled automatically", "pay again"
		}
		if strings.Count(body, cancelled) != 2 {
			t.Errorf("%s: /payment and /shipping do not both say a lapsed order is %q", locale, cancelled)
		}
		if strings.Contains(body, retry) {
			t.Errorf("%s: a policy still promises %q after the hold lapses", locale, retry)
		}
	}
}

// TestActiveInvoiceFAQDoesNotCallTheIssuerUnbuilt holds /faq to the issuer
// that is already wired. A missing merchant id is a deployment, not an
// unfinished product; saying 「尚未完成」 while Gateway.Issue exists is two
// answers to one customer question.
func TestActiveInvoiceFAQDoesNotCallTheIssuerUnbuilt(t *testing.T) {
	t.Parallel()

	g, err := issuer.NewGateway("", "", "", "")
	if err != nil {
		t.Fatalf("an empty configuration must be legal: %v", err)
	}
	if _, issueErr := g.Issue(t.Context(), issuer.IssueRequest{}); !errors.Is(issueErr, issuer.ErrDisabled) {
		t.Fatalf("Gateway.Issue is not wired: %v", issueErr)
	}

	seed, err := os.ReadFile(filepath.Join("..", "..", "seed", "dev_catalog.sql"))
	if err != nil {
		t.Fatalf("read the seed: %v", err)
	}
	src := string(seed)
	answer := struct{ zh, en string }{
		zh: sqlStringAfter(t, src, "('發票', '發票怎麼開立？',"),
		en: sqlStringAfter(t, src, "'How is my invoice issued?',"),
	}

	for loc, text := range map[string]string{"zh-Hant": answer.zh, "en": answer.en} {
		if strings.Contains(text, "尚未完成") || strings.Contains(text, "not built yet") {
			t.Errorf("%s invoice FAQ still says the issuer is unfinished: %q", loc, text)
		}
		if strings.Contains(text, "successfully issued") ||
			strings.Contains(text, "已成功開立") {
			t.Errorf("%s invoice FAQ invents a successful filing: %q", loc, text)
		}
	}
	if !strings.Contains(answer.zh, "綠界") || !strings.Contains(answer.zh, "設定") {
		t.Errorf("Chinese invoice FAQ does not describe ECPay as a deployment: %q", answer.zh)
	}
	if !strings.Contains(answer.en, "ECPay") || !strings.Contains(answer.en, "credentials") {
		t.Errorf("English invoice FAQ does not describe ECPay as a deployment: %q", answer.en)
	}
	// Capture queues the issue: the copy must not have a buyer wait on staff.
	if !strings.Contains(answer.zh, "付款完成") || !strings.Contains(answer.zh, "自動開立") {
		t.Errorf("Chinese invoice FAQ does not say payment issues the invoice: %q", answer.zh)
	}
	if !strings.Contains(answer.en, "automatically") || !strings.Contains(answer.en, "payment completes") {
		t.Errorf("English invoice FAQ does not say payment issues the invoice: %q", answer.en)
	}

	// The statutory return row is a different authority. Editing it here
	// would reopen 消保法 §19.
	if !strings.Contains(src, "退貨運費由 goen 負擔") ||
		!strings.Contains(src, "Rescinding within seven days, counted from the day after you receive the goods, costs you nothing") {
		t.Error("the statutory return FAQ was edited")
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
		cart.CookieName:              {zh: "購物車", en: "your cart"},
		account.SessionCookieName:    {zh: "登入狀態", en: "your sign-in"},
		access.CookieName:            {zh: "訂單瀏覽權限", en: "permission to view an order"},
		cart.PickupCookieName:        {zh: "挑選超商取貨門市", en: "picking a convenience store"},
		i18n.CookieName:              {zh: "你選擇的語言", en: "the language you chose"},
		home.DismissCookie:           {zh: "你關閉過的網站公告", en: "which site notice you have dismissed"},
		"__Host-goen_signin_context": {zh: "重設密碼後或刪除帳號前重新登入", en: "prefill sign-in after a password reset"},
		"__Host-goen_oauth":          {zh: "用 Google 登入時暫存", en: "while you sign in with Google"},
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
	for path, stored := range policies {
		doc := stored.For(i18n.ZhHant, testWindows)
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

// The i18n register test cannot reach these documents.
func TestPolicyDocumentsSayNiNotNin(t *testing.T) {
	for path, doc := range policies {
		texts := []string{doc.Title, doc.Summary}
		for _, s := range doc.Sections {
			texts = append(append(texts, s.Heading), s.Body...)
		}
		for _, text := range texts {
			if strings.Contains(text, "您") {
				t.Errorf("/%s says 您 where goen says 你: %q", path, text)
			}
		}
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

// TestReturnsHowToAskDoesNotRequireAReasonUnlessTheFormDoes holds the
// Chinese /returns "怎麼申請" steps to the form. Consumer Protection
// Act §19 I needs no reason; the customer textarea has no required,
// so a Chinese reader told to fill a reason before sending will think
// a blank is refused.
func TestReturnsHowToAskDoesNotRequireAReasonUnlessTheFormDoes(t *testing.T) {
	t.Parallel()

	zhHow := returnsHowToAsk(t, i18n.ZhHant)
	enHow := returnsHowToAsk(t, i18n.En)
	zhHTML := renderReturnsPolicy(t, i18n.ZhHant)
	enHTML := renderReturnsPolicy(t, i18n.En)

	if !strings.Contains(zhHTML, zhHow.heading) {
		t.Errorf("Chinese /returns does not render the how-to-ask heading %q", zhHow.heading)
	}
	if !strings.Contains(enHTML, enHow.heading) {
		t.Errorf("English /returns does not render the how-to-ask heading %q", enHow.heading)
	}
	for _, para := range zhHow.body {
		if !strings.Contains(zhHTML, para) {
			t.Errorf("Chinese /returns does not render how-to-ask %q", para)
		}
	}
	if !strings.Contains(enHTML, "A reason is optional") {
		t.Errorf("English /returns does not render the optional-reason sentence")
	}

	if returnReasonFieldIsRequired(t) {
		return
	}

	impliedRequired := []string{"填寫原因後送出", "填寫原因後", "必須填寫原因", "請填寫原因"}
	for _, phrase := range impliedRequired {
		if strings.Contains(zhHow.text, phrase) {
			t.Errorf("Chinese how-to-ask implies a reason is required: %q", phrase)
		}
		if strings.Contains(zhHTML, phrase) {
			t.Errorf("rendered Chinese /returns still implies a required reason: %q", phrase)
		}
	}
	if !strings.Contains(zhHow.text, "原因選填") {
		t.Error("Chinese how-to-ask does not say the reason is optional")
	}
	if !strings.Contains(zhHTML, "原因選填") {
		t.Error("rendered Chinese /returns does not say the reason is optional")
	}
	if !strings.Contains(enHow.text, "A reason is optional") {
		t.Error("English how-to-ask no longer says a reason is optional")
	}
}

type howToAskCopy struct {
	heading string
	body    []string
	text    string
}

func returnsHowToAsk(t *testing.T, locale i18n.Locale) howToAskCopy {
	t.Helper()

	doc, ok := policies["returns"]
	if !ok {
		t.Fatal("no /returns policy")
	}
	localized := doc.For(locale, testWindows)
	want := "怎麼申請"
	if locale == i18n.En {
		want = "How to ask"
	}
	for _, s := range localized.Sections {
		if s.Heading != want {
			continue
		}
		return howToAskCopy{
			heading: s.Heading,
			body:    s.Body,
			text:    strings.Join(s.Body, "\n"),
		}
	}
	t.Fatalf("/returns has no %q section", want)
	return howToAskCopy{}
}

func renderReturnsPolicy(t *testing.T, locale i18n.Locale) string {
	t.Helper()

	doc, ok := policies["returns"]
	if !ok {
		t.Fatal("no /returns policy")
	}
	doc = doc.For(locale, testWindows)
	var b strings.Builder
	if err := Policy(
		layouts.Page{Title: doc.Title, Description: doc.Summary},
		doc,
	).Render(i18n.WithLocale(t.Context(), locale), &b); err != nil {
		t.Fatalf("render /returns: %v", err)
	}
	out := b.String()
	if out == "" {
		t.Fatal("rendered /returns is empty")
	}
	return out
}

// returnReasonFieldIsRequired reports the customer return form's reason
// textarea. The policy may tell a reader a reason is required only when
// this control does.
func returnReasonFieldIsRequired(t *testing.T) bool {
	t.Helper()

	src, err := os.ReadFile(filepath.Join("..", "ui", "pages", "returns.templ"))
	if err != nil {
		t.Fatalf("read returns.templ: %v", err)
	}
	textareas := regexp.MustCompile(`(?s)<textarea\b[^>]*>`).FindAllString(string(src), -1)
	for _, tag := range textareas {
		if !strings.Contains(tag, `id="return-reason"`) {
			continue
		}
		return regexp.MustCompile(`(?:^|[\s{])required(?:$|[\s}])`).MatchString(tag)
	}
	t.Fatal("returns.templ has no #return-reason textarea; the lock is not reading the form")
	return false
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

	for path, stored := range policies {
		en, doc := stored.For(i18n.En, testWindows), stored.For(i18n.ZhHant, testWindows)
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
			want:   []string{"自收到商品的次日起七日內", "任何約定都不能縮短或排除"},
			wantEn: []string{"seven days to cancel", "counted from the day after you receive the goods", "no agreement can shorten or waive it"},
		},
		{
			term:   "who pays return postage",
			cite:   "消保法 §19 I — the consumer bears 任何費用, which is to say none",
			doc:    "returns",
			want:   []string{"退貨運費由 goen 負擔", "整筆訂單都退回時，原本支付的運費也會退還給你"},
			wantEn: []string{"return postage included", "The delivery fee you originally paid is refunded once you have returned the whole order"},
		},
		{
			term:   "whether opening the box forfeits the right",
			cite:   "通訊交易解除權合理例外情事適用準則 §2 — a closed list of seven, and opening the parcel to inspect the goods is on none of them",
			doc:    "returns",
			want:   []string{"拆開包裹檢查商品都不會讓七日的解除權結束"},
			wantEn: []string{"opening the parcel to inspect the goods does not end your seven days"},
		},
		{
			term:   "whether a request inside the window can be declined",
			cite:   "消保法 §19 I — 無須說明理由, and §19 IV ends the contract on dispatch; returns.Evaluate refuses a decline on any statutory line",
			doc:    "returns",
			want:   []string{"商品送達前，或在猶豫期內提出的申請，我們不會拒絕。"},
			wantEn: []string{"A request made before the goods arrive, or within your seven-day right to cancel, is never declined."},
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

// TestReturnsCountsTheStatutoryWindowInTheStatutesWord holds /returns to 七日,
// the word §19 I uses, so one page does not call the same right two names.
func TestReturnsCountsTheStatutoryWindowInTheStatutesWord(t *testing.T) {
	t.Parallel()

	for _, sec := range policies["returns"].Sections {
		for _, text := range append([]string{sec.Heading}, sec.Body...) {
			if strings.Contains(text, "七天") {
				t.Errorf("/returns says 七天 where §19 I says 七日: %q", text)
			}
		}
	}
}

// TestTheShippingPageStatesTheSurchargeItCharges holds that the page says where
// costs extra, in the visitor's language.
func TestTheShippingPageStatesTheSurchargeItCharges(t *testing.T) {
	t.Parallel()

	m := ShippingMethod{
		Name: "宅配到府", FeeCents: 8000, FreeOverCents: 300000,
		Surcharges: []ZoneSurcharge{
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
			want:   "離島: NT$200 extra, 澎湖: NT$150 extra",
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

	plain := ShippingMethod{Name: "超商取貨", FeeCents: 6000}
	if plain.HasSurcharges() {
		t.Error("a method with no surcharge reports one")
	}
	if plain.SurchargeText(i18n.WithLocale(t.Context(), i18n.En)) != "" {
		t.Error("a method with no surcharge still says something")
	}
}
