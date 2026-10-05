package layouts_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func renderChrome(t *testing.T, locale i18n.Locale, items []layouts.NavItem) (header, footer string) {
	t.Helper()
	ctx := layouts.WithTopNav(i18n.WithLocale(t.Context(), locale), items)
	var h, f strings.Builder
	if err := layouts.Header(layouts.Page{}).Render(ctx, &h); err != nil {
		t.Fatalf("render header: %v", err)
	}
	if err := layouts.Footer(layouts.NewsletterState{}).Render(ctx, &f); err != nil {
		t.Fatalf("render footer: %v", err)
	}
	return h.String(), f.String()
}

var chromeNav = []layouts.NavItem{
	{Slug: "phones", Name: "手機", Href: "/c/phones"},
	{Slug: "accessories", Name: "周邊配件", Href: "/c/accessories", Children: []layouts.NavItem{
		{Slug: "chargers", Name: "充電與線材", Href: "/c/chargers"},
	}, Picks: []layouts.NavPick{
		{Slug: "aurora-charger-65", Name: "Aurora GaN 65W 充電器", Price: "NT$990", ImageURL: "/static/aurora-charger-65-01.webp"},
		{Slug: "koto-cable-braided", Name: "Koto 編織 USB-C 線 2m", Price: "NT$490 起"},
	}},
}

// A screen reader reads a label in the language of the page unless the label
// says otherwise, so the language control's own names would be read in the
// wrong voice on the page that most needs them.
func TestTheLanguageSwitchLabelsCarryTheirOwnLanguage(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		_, footer := renderChrome(t, locale, chromeNav)
		for _, l := range i18n.Locales() {
			want := `lang="` + l.Tag() + `"`
			button := regexp.MustCompile(`<button[^>]*goen-lang__item[^>]*value="` + l.Tag() + `"[^>]*>`).FindString(footer)
			if !strings.Contains(button, want) {
				t.Errorf("%s page: the %s button is %q, want %s", locale, l.Tag(), button, want)
			}
		}
	}
}

// A form may only take the search, none or presentation roles, so the group the
// language control needs lives inside it.
func TestNoFormCarriesARoleItMayNotHave(t *testing.T) {
	t.Parallel()

	header, footer := renderChrome(t, i18n.ZhHant, chromeNav)
	for _, form := range regexp.MustCompile(`<form[^>]*>`).FindAllString(header+footer, -1) {
		if strings.Contains(form, `role="group"`) {
			t.Errorf("form %q takes role=group", form)
		}
	}
	if !strings.Contains(footer, `role="group"`) {
		t.Error("the language control lost its group")
	}
	if !strings.Contains(header, `role="search" aria-label=`) {
		t.Error("the header search is an unnamed landmark")
	}
}

// The header's language control is one menu button and, once opened, one
// choice per language, in the bar and again in the drawer, which CSS shows one
// at a time; the footer keeps its own pair of plain buttons. Neither is a
// second copy of the other's markup, and the button says which language is on.
func TestTheLanguageControlIsAMenuInTheHeaderAndAPairInTheFooter(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		header, footer := renderChrome(t, locale, chromeNav)
		want := len(i18n.Locales())
		if got := strings.Count(header, "goen-langmenu__item"); got != 2*want {
			t.Errorf("%s: the bar and the drawer have %d choices, want %d", locale, got, 2*want)
		}
		if strings.Contains(header, "goen-lang__item") {
			t.Errorf("%s: the header carries the footer's pair as well as its menu", locale)
		}
		if got := strings.Count(footer, "goen-lang__item"); got != want {
			t.Errorf("%s: the footer has %d language buttons, want %d", locale, got, want)
		}
		if !strings.Contains(header, `lang="`+locale.Tag()+`">`+locale.Short()+`</span>`) {
			t.Errorf("%s: the menu button does not show %q", locale, locale.Short())
		}
		if got := strings.Count(header, `aria-pressed="true"`); got != 2 {
			t.Errorf("%s: %d choices are marked current, want one per menu", locale, got)
		}
	}
}

// A phone's bar has no room for the wishlist, the account or the language, so
// the drawer carries them, and it carries a way out that has a name.
func TestTheDrawerCarriesWhatThePhoneBarHasNoRoomFor(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		header, _ := renderChrome(t, locale, chromeNav)
		start := strings.Index(header, `class="goen-header__drawer"`)
		end := strings.Index(header, `class="goen-header__brand"`)
		if start < 0 || end < start {
			t.Fatalf("%s: the drawer is not in the header", locale)
		}
		drawer := header[start:end]
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, want := range []string{
			`href="/account/wishlist">` + i18n.T(ctx, i18n.KeyWishlist) + `</a>`,
			`href="/account">` + i18n.T(ctx, i18n.KeyAccount) + `</a>`,
			`aria-label="` + i18n.T(ctx, i18n.KeyCloseMenu) + `"`,
			"data-menu-close",
			"goen-langmenu__item",
		} {
			if !strings.Contains(drawer, want) {
				t.Errorf("%s: the drawer does not contain %q", locale, want)
			}
		}
	}
}

// The menu's choices carry their own language for the same reason the
// footer's do: each is the language's name for itself.
func TestTheLanguageMenuChoicesCarryTheirOwnLanguage(t *testing.T) {
	t.Parallel()

	header, _ := renderChrome(t, i18n.En, chromeNav)
	for _, l := range i18n.Locales() {
		button := regexp.MustCompile(`<button[^>]*goen-langmenu__item[^>]*value="` + l.Tag() + `"[^>]*>`).FindString(header)
		if !strings.Contains(button, `lang="`+l.Tag()+`"`) {
			t.Errorf("the %s choice is %q, want lang=%q", l.Tag(), button, l.Tag())
		}
	}
}

// A department with sub-categories opens a panel listing them; one without is
// a plain link and has no panel to open.
func TestOnlyADepartmentWithChildrenOpensAPanel(t *testing.T) {
	t.Parallel()

	header, footer := renderChrome(t, i18n.ZhHant, chromeNav)
	if got := strings.Count(header, `class="goen-dept"`); got != 1 {
		t.Errorf("header has %d department panels, want 1", got)
	}
	for _, want := range []string{`class="goen-dept__link" href="/c/chargers"`, "逛逛周邊配件"} {
		if !strings.Contains(header, want) {
			t.Errorf("header does not contain %q", want)
		}
	}
	// The drawer lists every sub-category as a link of its own, so a phone
	// reaches them without a hover.
	if !strings.Contains(header, `goen-header__subitem`) {
		t.Error("the drawer does not list the sub-categories")
	}
	if !strings.Contains(footer, `href="/c/phones"`) {
		t.Error("the footer's department column does not link the departments")
	}
}

// The first thing a keyboard reaches is the way past the header, and what it
// names exists: a skip link to an id nothing carries does nothing.
func TestTheSkipLinkIsTheFirstTabStopAndNamesTheMainLandmark(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	if err := layouts.Base(layouts.Page{Title: "t"}).Render(templ.WithChildren(ctx, templ.NopComponent), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := b.String()
	bodyAt := strings.Index(body, "<body")
	if bodyAt < 0 {
		t.Fatal("the page has no <body")
	}
	body = body[bodyAt:]
	firstLink := strings.Index(body, "<a ")
	skip := strings.Index(body, `<a class="goen-skip" href="#main">`)
	if skip < 0 || skip != firstLink {
		t.Errorf("the skip link is at %d and the first link at %d, want the same", skip, firstLink)
	}
	if !strings.Contains(body, `<main id="main">`) {
		t.Error("nothing carries the id the skip link names")
	}
}

// A department panel offers a few of its products as links of their own, each
// with its price, so a department with two sub-categories is not an empty
// sheet. Their pictures are lazy: the panel is display:none until it opens,
// and an eager image there is a fetch on every page for a panel nobody opened.
func TestADepartmentPanelOffersItsProducts(t *testing.T) {
	t.Parallel()

	header, _ := renderChrome(t, i18n.ZhHant, chromeNav)
	for _, want := range []string{
		`<a class="goen-dept__pick" href="/p/aurora-charger-65">`,
		`<span class="goen-dept__name">Aurora GaN 65W 充電器</span>`,
		`<span class="goen-dept__price">NT$990</span>`,
		`<span class="goen-dept__price">NT$490 起</span>`,
	} {
		if !strings.Contains(header, want) {
			t.Errorf("the panel does not contain %q", want)
		}
	}
	picksAt := strings.Index(header, `class="goen-dept__picks"`)
	if picksAt < 0 {
		t.Fatal("the panel has no products list")
	}
	picks := header[picksAt:]
	listEnd := strings.Index(picks, "</ul>")
	if listEnd < 0 {
		t.Fatal("the products list is never closed")
	}
	picks = picks[:listEnd]
	imgs := regexp.MustCompile(`<img[^>]*>`).FindAllString(picks, -1)
	if len(imgs) != 1 {
		t.Fatalf("the panel draws %d product pictures, want 1 — a product with no picture "+
			"keeps its well empty rather than a broken image", len(imgs))
	}
	if !strings.Contains(imgs[0], `loading="lazy"`) {
		t.Errorf("a panel picture is fetched before the panel opens: %s", imgs[0])
	}
}
