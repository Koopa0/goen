package account

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/uitest"
	"github.com/koopa0/goen/internal/web/formtext"
)

// enclosing is the nearest ancestor of n with the tag, or nil.
func enclosing(n *html.Node, tag string) *html.Node {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && p.Data == tag {
			return p
		}
	}
	return nil
}

func formByAction(doc *html.Node, action string) *html.Node {
	return findDescendant(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "form" && attrValue(n, "action") == action
	})
}

// TestAccountProfileRefusal: a refused profile keeps what was typed, marks each
// refused control and says under it why, in both languages.
func TestAccountProfileRefusal(t *testing.T) {
	t.Parallel()
	longName := strings.Repeat("名", 61)
	longPhone := strings.Repeat("1", 31)
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			v := &View{
				Email: "shopper@example.com", Name: "王小明", Phone: "0912345678",
				ProfileDraft:  &ProfileDraft{Name: longName, Phone: longPhone},
				ProfileErrors: formtext.FieldMessages(ctx, profileRefusals(longName, longPhone)),
			}
			body := renderComponent(t, ctx, Account(Meta(ctx), v))
			uitest.AssertRefusedInput(t, body, "acct-name", longName)
			uitest.AssertRefusedInput(t, body, "acct-phone", longPhone)
			for _, key := range []i18n.Key{i18n.KeyNameTooLong, i18n.KeyPhoneMalformed} {
				if want := i18n.T(ctx, key); !strings.Contains(body, ">"+want+"<") {
					t.Errorf("the refused profile does not say %q under its field", want)
				}
			}
		})
	}
}

// TestAccountProfileUnrefused: a plain visit shows the saved profile, with no
// control marked.
func TestAccountProfileUnrefused(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	body := renderComponent(t, ctx, Account(Meta(ctx), &View{Name: "王小明", Phone: "0912345678"}))
	if !strings.Contains(body, `value="王小明"`) || !strings.Contains(body, `value="0912345678"`) {
		t.Error("the profile form does not show the saved name and phone")
	}
	if strings.Contains(body, "aria-invalid") {
		t.Error("a plain visit marks a control invalid")
	}
}

// TestAccountCredentialRefusal: a credential form auth refused reopens, keeps
// what was typed except a password, and says under each control why.
func TestAccountCredentialRefusal(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		refusal auth.AccountRefusal
		action  string
		// invalid maps a control's id to the raw text it must show.
		invalid map[string]string
	}{
		{
			name: "password",
			refusal: auth.AccountRefusal{Form: auth.PasswordForm, Errors: map[string]string{
				"current": "wrong", "password": "short", "confirm": "differ",
			}},
			action:  "/account/password#password-heading",
			invalid: map[string]string{"pw-current": "", "pw-new": "", "pw-confirm": ""},
		},
		{
			name: "email",
			refusal: auth.AccountRefusal{
				Form:   auth.EmailForm,
				Errors: map[string]string{"email": "malformed", "current": "wrong"},
				Values: map[string]string{"email": "not-an-address"},
			},
			action:  "/account/email#email-heading",
			invalid: map[string]string{"new-email": "not-an-address", "email-current": ""},
		},
		{
			name: "erase",
			refusal: auth.AccountRefusal{
				Form:   auth.EraseForm,
				Errors: map[string]string{"confirm": "type the address"},
				Values: map[string]string{"confirm": "someone@else.example"},
			},
			action:  "/account/erase#erase-heading",
			invalid: map[string]string{"erase-confirm": "someone@else.example"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), i18n.En)
			v := &View{Email: "shopper@example.com", HasPassword: true, Refusal: &tt.refusal}
			body := renderComponent(t, ctx, Account(Meta(ctx), v))
			for id, raw := range tt.invalid {
				uitest.AssertRefusedInput(t, body, id, raw)
			}
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			form := formByAction(doc, tt.action)
			if form == nil {
				t.Fatalf("no form posts to %s", tt.action)
			}
			if d := enclosing(form, "details"); d == nil || !hasAttr(d, "open") {
				t.Errorf("the %s form is closed after it was refused", tt.name)
			}
			if n := strings.Count(body, `aria-invalid="true"`); n != len(tt.invalid) {
				t.Errorf("%d controls are marked invalid, want %d", n, len(tt.invalid))
			}
		})
	}
}

// TestAccountCredentialUnrefused: with no refusal every credential form stays
// closed and unmarked, and no control shows a value.
func TestAccountCredentialUnrefused(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	body := renderComponent(t, ctx, Account(Meta(ctx), &View{Email: "shopper@example.com", HasPassword: true}))
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"/account/password#password-heading", "/account/email#email-heading", "/account/erase#erase-heading"} {
		form := formByAction(doc, action)
		if form == nil {
			t.Fatalf("no form posts to %s", action)
		}
		if d := enclosing(form, "details"); d == nil || hasAttr(d, "open") {
			t.Errorf("the form to %s starts open on a plain visit", action)
		}
	}
	if strings.Contains(body, "aria-invalid") || strings.Contains(body, "-refusal") {
		t.Error("a plain visit shows a refusal")
	}
}

// TestAccountRefusalNotice: a refusal that no single control explains is said
// in its own section, as an alert, where the page opens.
func TestAccountRefusalNotice(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, tt := range []struct {
		name    string
		refusal auth.AccountRefusal
		order   string
		section string
		want    string
	}{
		{
			name:    "staff address",
			refusal: auth.AccountRefusal{Form: auth.EmailForm, Notice: i18n.T(ctx, i18n.KeyEmailStaffFixed)},
			section: "email-heading",
			want:    i18n.T(ctx, i18n.KeyEmailStaffFixed),
		},
		{
			name:    "demo password",
			refusal: auth.AccountRefusal{Form: auth.PasswordForm, Notice: i18n.T(ctx, i18n.KeyDemoAccountFixed)},
			section: "password-heading",
			want:    i18n.T(ctx, i18n.KeyDemoAccountFixed),
		},
		{
			name:    "last admin",
			refusal: auth.AccountRefusal{Form: auth.EraseForm, Notice: i18n.T(ctx, i18n.KeyEraseLastAdmin)},
			section: "erase-heading",
			want:    i18n.T(ctx, i18n.KeyEraseLastAdmin),
		},
		{
			name:    "return left unnamed",
			refusal: auth.AccountRefusal{Form: auth.EraseForm, Notice: i18n.T(ctx, i18n.KeyEraseOpenReturn), OpenReturn: true},
			section: "erase-heading",
			want:    i18n.T(ctx, i18n.KeyEraseOpenReturn),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := &View{Email: "shopper@example.com", HasPassword: true, Refusal: &tt.refusal}
			doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, Account(Meta(ctx), v))))
			if err != nil {
				t.Fatal(err)
			}
			notice := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && attrValue(n, "id") == string(tt.refusal.Form)+"-refusal"
			})
			if notice == nil {
				t.Fatalf("the %s section does not say why it was refused", tt.refusal.Form)
			}
			if got := strings.TrimSpace(nodeText(notice)); got != tt.want {
				t.Errorf("notice = %q, want %q", got, tt.want)
			}
			if attrValue(notice, "role") != "alert" || !strings.Contains(attrValue(notice, "class"), "goen-notice--danger") {
				t.Errorf("notice has role %q and class %q, want a danger alert", attrValue(notice, "role"), attrValue(notice, "class"))
			}
			section := enclosing(notice, "section")
			if section == nil || attrValue(section, "aria-labelledby") != tt.section {
				t.Errorf("notice is not inside the section labelled %s", tt.section)
			}
			if findDescendant(notice, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "a" }) != nil {
				t.Error("a notice that names no order links somewhere")
			}
		})
	}
}

// TestAccountRefusalReturnLink: an erasure refused for a return names its order
// in the ruled sentence, and the number links to the order's page.
func TestAccountRefusalReturnLink(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.En, "A return on GO-0042 is still being processed. You can delete your account once it's finished."},
		{i18n.ZhHant, "你有一筆退貨還在處理（GO-0042），完成後才能刪除帳號。"},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			v := &View{
				Email:       "shopper@example.com",
				Refusal:     &auth.AccountRefusal{Form: auth.EraseForm, Notice: i18n.T(ctx, i18n.KeyEraseOpenReturn), OpenReturn: true},
				ReturnOrder: "GO-0042",
			}
			doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, Account(Meta(ctx), v))))
			if err != nil {
				t.Fatal(err)
			}
			notice := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && attrValue(n, "id") == "erase-refusal"
			})
			if notice == nil {
				t.Fatal("the erase section does not say why it was refused")
			}
			if got := strings.TrimSpace(nodeText(notice)); got != tt.want {
				t.Errorf("notice = %q, want %q", got, tt.want)
			}
			link := findDescendant(notice, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "a" })
			if link == nil || attrValue(link, "href") != "/orders/GO-0042" || nodeText(link) != "GO-0042" {
				t.Errorf("the order number is not a link to /orders/GO-0042: %v", link)
			}
		})
	}
}

// TestAccountFormAnchors: every form that can be refused posts to its section,
// so the page opens there, and each section's heading carries the id.
func TestAccountFormAnchors(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, Account(Meta(ctx), &View{Email: "shopper@example.com", HasPassword: true}))))
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{
		"/account/profile#profile-heading",
		"/account/addresses#addresses-heading",
		"/account/email/resend#email-heading",
		"/account/email#email-heading",
		"/account/password#password-heading",
		"/account/erase#erase-heading",
	} {
		if formByAction(doc, action) == nil {
			t.Errorf("no form posts to %s", action)
			continue
		}
		_, id, _ := strings.Cut(action, "#")
		if findDescendant(doc, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "h2" && attrValue(n, "id") == id
		}) == nil {
			t.Errorf("%s names no section heading", action)
		}
	}
}
