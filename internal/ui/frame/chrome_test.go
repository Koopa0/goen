package frame_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

func renderChrome(t *testing.T, locale i18n.Locale, items []frame.NavItem) (header, footer string) {
	t.Helper()
	ctx := frame.WithTopNav(i18n.WithLocale(t.Context(), locale), items)
	var h, f strings.Builder
	if err := frame.Header(frame.Page{}).Render(ctx, &h); err != nil {
		t.Fatalf("render header: %v", err)
	}
	if err := frame.Footer(frame.NewsletterState{}).Render(ctx, &f); err != nil {
		t.Fatalf("render footer: %v", err)
	}
	return h.String(), f.String()
}

// drawerOf is the menu's drawer in a rendered header: from its <nav> to the
// </nav> that closes it, which holds no navigation of its own.
func drawerOf(t *testing.T, header string) string {
	t.Helper()
	start := strings.Index(header, `class="goen-header__drawer"`)
	if start < 0 {
		t.Fatal("the drawer is not in the header")
	}
	drawer, _, closed := strings.Cut(header[start:], "</nav>")
	if !closed {
		t.Fatal("the drawer is never closed")
	}
	return drawer
}

var chromeNav = []frame.NavItem{
	{Slug: "phones", Name: "手機", Href: "/c/phones"},
	{Slug: "accessories", Name: "周邊配件", Href: "/c/accessories", Children: []frame.NavItem{
		{Slug: "chargers", Name: "充電與線材", Href: "/c/chargers"},
	}},
}

// TestFooterLanguageSwitchLang: the language switch labels carry their own language.
//
// A screen reader reads a label in the language of the page unless the label
// says otherwise, so the language control's own names would be read in the
// wrong voice on the page that most needs them.
func TestFooterLanguageSwitchLang(t *testing.T) {
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

// TestHeaderFooterFormRoles: no form carries a role it may not have.
//
// A form may only take the search, none or presentation roles, so the group the
// language control needs lives inside it.
func TestHeaderFooterFormRoles(t *testing.T) {
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

// TestHeaderLanguageMenuFooterPair: the language control is a word in the bar, a menu in the drawer and a pair in the footer.
//
// The bar names the other language and switches to it in one press; the
// drawer, which the bar below 1024 leaves it to, keeps a menu of one choice per
// language; the footer keeps its own pair of plain buttons. None is a second
// copy of another's markup, and the menu says which language is on.
func TestHeaderLanguageMenuFooterPair(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		header, footer := renderChrome(t, locale, chromeNav)
		want := len(i18n.Locales())
		if got := strings.Count(header, "goen-langmenu__item"); got != want {
			t.Errorf("%s: the header has %d language choices, want the drawer menu's %d", locale, got, want)
		}
		if got := strings.Count(drawerOf(t, header), "goen-langmenu__item"); got != want {
			t.Errorf("%s: %d of the language choices are in the drawer, want all %d", locale, got, want)
		}
		if strings.Contains(header, "goen-lang__item") {
			t.Errorf("%s: the header carries the footer's pair as well as its menu", locale)
		}
		if got := strings.Count(footer, "goen-lang__item"); got != want {
			t.Errorf("%s: the footer has %d language buttons, want %d", locale, got, want)
		}
		var other i18n.Locale
		for _, l := range i18n.Locales() {
			if l != locale {
				other = l
			}
		}
		bar := regexp.MustCompile(`(?s)<form class="goen-header__lang"[^>]*>.*?</form>`).FindString(header)
		if !strings.Contains(bar, `name="locale" value="`+other.Tag()+`"`) || strings.Count(bar, "<button") != 1 {
			t.Errorf("%s: the bar's language control is %q, want one button posting %s", locale, bar, other.Tag())
		}
		if !strings.Contains(bar, `<span lang="`+other.Tag()+`">`+other.Short()+`</span>`) {
			t.Errorf("%s: the bar's language control does not show %q in its own language: %s", locale, other.Short(), bar)
		}
		if got := strings.Count(header, `lang="`+other.Tag()+`">`+other.Short()+`</span>`); got != 2 {
			t.Errorf("%s: %d controls show %q, want the bar's and the drawer menu's", locale, got, other.Short())
		}
		if got := strings.Count(header, `aria-pressed="true"`); got != 1 {
			t.Errorf("%s: %d choices are marked current, want the drawer menu's one", locale, got)
		}
	}
}

// TestHeaderDrawerContents: the drawer carries what the phone bar has no room for.
//
// The bar below 1024 has no room for the wishlist, the account or the language,
// so the drawer carries them, and it carries a way out that has a name.
func TestHeaderDrawerContents(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		header, _ := renderChrome(t, locale, chromeNav)
		drawer := drawerOf(t, header)
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

// TestHeaderBarWords: the bar names its controls in words.
//
// The menu, comparing, the account and the cart are words a shopper reads.
// Comparing's glyph stands beside its word, and the phone's search button,
// the one control drawn as a glyph alone, carries its word for a screen
// reader, so no control is named by a picture alone.
func TestHeaderBarWords(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		header, _ := renderChrome(t, locale, chromeNav)
		ctx := i18n.WithLocale(t.Context(), locale)
		bar, _, found := strings.Cut(header, "</header>")
		if !found {
			t.Fatalf("%s: the header is never closed", locale)
		}
		for _, want := range []string{
			`data-menu-open aria-haspopup="dialog" aria-controls="site-menu">` + i18n.T(ctx, i18n.KeyMenu) + `</button>`,
			`href="/account">` + i18n.T(ctx, i18n.KeyAccount) + `</a>`,
			`aria-hidden="true">` + i18n.T(ctx, i18n.KeyCart),
		} {
			if !strings.Contains(bar, want) {
				t.Errorf("%s: the bar does not contain %q", locale, want)
			}
		}
		compare := regexp.MustCompile(`(?s)<a class="goen-header__tool goen-header__compare" href="/compare">(.*?)</a>`).FindStringSubmatch(bar)
		if compare == nil {
			t.Fatalf("%s: the bar has no link to compare", locale)
		}
		if !strings.Contains(compare[1], i18n.T(ctx, i18n.KeyCompareTitle)) || !strings.Contains(compare[1], `aria-hidden="true"`) {
			t.Errorf("%s: comparing is not its word beside a hidden glyph: %s", locale, compare[1])
		}
		if !regexp.MustCompile(`(?s)data-search-open[^>]*>\s*<svg[^>]*aria-hidden="true".*?</svg>\s*<span class="goen-sr-only">` + regexp.QuoteMeta(i18n.T(ctx, i18n.KeySearch)) + `</span>`).MatchString(bar) {
			t.Errorf("%s: the search button is not a hidden glyph with its word for a screen reader", locale)
		}
		if got := strings.Count(bar, "<svg"); got != 4 {
			t.Errorf("%s: the bar draws %d glyphs, want the mark, the search button's and the field's magnifiers and comparing's", locale, got)
		}
	}
}

// TestHeaderLanguageMenuLang: the language menu choices carry their own language.
//
// The menu's choices carry their own language for the same reason the
// footer's do: each is the language's name for itself.
func TestHeaderLanguageMenuLang(t *testing.T) {
	t.Parallel()

	header, _ := renderChrome(t, i18n.En, chromeNav)
	for _, l := range i18n.Locales() {
		button := regexp.MustCompile(`<button[^>]*goen-langmenu__item[^>]*value="` + l.Tag() + `"[^>]*>`).FindString(header)
		if !strings.Contains(button, `lang="`+l.Tag()+`"`) {
			t.Errorf("the %s choice is %q, want lang=%q", l.Tag(), button, l.Tag())
		}
	}
}

func renderHeader(t *testing.T, items []frame.NavItem, deals bool, page frame.Page) string {
	t.Helper()
	ctx := frame.WithDeals(frame.WithTopNav(i18n.WithLocale(t.Context(), i18n.ZhHant), items), deals)
	var b strings.Builder
	if err := frame.Header(page).Render(ctx, &b); err != nil {
		t.Fatalf("render header: %v", err)
	}
	return b.String()
}

// TestHeaderDealsLinkOnlyWithDeals: the header offers deals only when there is something to buy.
//
// 優惠 is offered when the deals page has something to buy and not otherwise.
func TestHeaderDealsLinkOnlyWithDeals(t *testing.T) {
	t.Parallel()

	for _, deals := range []bool{true, false} {
		header := renderHeader(t, chromeNav, deals, frame.Page{})
		if got := strings.Contains(header, `href="/deals"`); got != deals {
			t.Errorf("deals=%v: the header links /deals = %v", deals, got)
		}
		if got := strings.Count(header, `href="/deals"`); deals && got != 2 {
			t.Errorf("deals=%v: /deals is linked %d times, want once in the row and once in the menu", deals, got)
		}
	}
}

// TestHeaderAriaCurrent: the header marks the current deals page.
func TestHeaderAriaCurrent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		path        string
		nav         string
		deals       bool
		wantCurrent string
	}{
		{name: "deals", path: "/deals", deals: true, wantCurrent: "/deals"},
		{name: "deals pagination", path: "/deals?page=2", deals: true, wantCurrent: "/deals"},
		{name: "home", path: "/", deals: true},
		{name: "search query", path: "/search?q=/deals", deals: true},
		{name: "department", path: "/c/phones", nav: "phones", deals: true, wantCurrent: "/c/phones"},
		{name: "similar prefix", path: "/deals-extra", deals: true},
		{name: "nested path", path: "/deals/offers", deals: true},
		{name: "trailing slash", path: "/deals/", deals: true},
		{name: "absent request path", deals: true},
		{name: "no deals", path: "/deals", deals: false},
		{name: "department without deals", path: "/c/phones", nav: "phones", deals: false, wantCurrent: "/c/phones"},
	}
	navs := regexp.MustCompile(`(?s)<nav class="(goen-header__drawer|goen-header__nav)"[^>]*>(.*?)</nav>`)
	anchors := regexp.MustCompile(`<a\b[^>]*>`)
	hrefs := regexp.MustCompile(`href="([^"]*)"`)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, origin := range []string{"", "https://shop.example"} {
			for _, tt := range tests {
				t.Run(locale.Tag()+"/"+origin+"/"+tt.name, func(t *testing.T) {
					t.Parallel()
					ctx := frame.WithDeals(frame.WithTopNav(i18n.WithLocale(t.Context(), locale), chromeNav), tt.deals)
					ctx = frame.WithReturnPath(ctx, tt.path)
					if origin != "" {
						ctx = frame.WithRequestPath(frame.WithSiteOrigin(ctx, origin), "/")
					}
					var b strings.Builder
					if err := frame.Header(frame.Page{Nav: tt.nav}).Render(ctx, &b); err != nil {
						t.Fatalf("render header: %v", err)
					}
					sets := navs.FindAllStringSubmatch(b.String(), -1)
					if len(sets) != 2 {
						t.Fatalf("navigation sets = %d, want drawer and desktop row", len(sets))
					}
					for _, set := range sets {
						var current []string
						dealLinks := 0
						for _, anchor := range anchors.FindAllString(set[2], -1) {
							href := hrefs.FindStringSubmatch(anchor)
							if len(href) != 2 {
								continue
							}
							if strings.Contains(anchor, `aria-current="page"`) {
								current = append(current, href[1])
							}
							if href[1] == "/deals" {
								dealLinks++
								if !strings.Contains(set[2], anchor+i18n.T(ctx, i18n.KeyDeals)+"</a>") {
									t.Errorf("%s: deals link lost its localized label", set[1])
								}
							}
						}
						wantLinks := 0
						if tt.deals {
							wantLinks = 1
						}
						if dealLinks != wantLinks {
							t.Errorf("%s: deals links = %d, want %d", set[1], dealLinks, wantLinks)
						}
						if tt.wantCurrent == "" {
							if len(current) != 0 {
								t.Errorf("%s: current links = %v, want none", set[1], current)
							}
						} else if len(current) != 1 || current[0] != tt.wantCurrent {
							t.Errorf("%s: current links = %v, want exactly [%s]", set[1], current, tt.wantCurrent)
						}
					}
				})
			}
		}
	}
}

// TestHeaderDepartmentRow: the department row is absent for one department and no deals.
//
// A shop with one department and nothing on offer has nothing to put in a
// second row.
func TestHeaderDepartmentRow(t *testing.T) {
	t.Parallel()

	one := chromeNav[:1]
	tests := []struct {
		name  string
		items []frame.NavItem
		deals bool
		want  bool
	}{
		{name: "one department, no deals", items: one, deals: false, want: false},
		{name: "one department, deals", items: one, deals: true, want: true},
		{name: "two departments, no deals", items: chromeNav, deals: false, want: true},
	}
	for _, tt := range tests {
		header := renderHeader(t, tt.items, tt.deals, frame.Page{})
		if got := strings.Contains(header, `class="goen-header__nav"`); got != tt.want {
			t.Errorf("%s: the department row is present = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestHeaderCurrentDepartmentMarked: the current department is marked.
func TestHeaderCurrentDepartmentMarked(t *testing.T) {
	t.Parallel()

	header := renderHeader(t, chromeNav, false, frame.Page{Nav: "phones"})
	_, row, found := strings.Cut(header, `class="goen-header__nav"`)
	if !found {
		t.Fatal("the header has no department row")
	}
	if !strings.Contains(row, `aria-current="page" href="/c/phones"`) {
		t.Error("the current department's link does not carry aria-current")
	}
	if strings.Contains(row, `aria-current="page" href="/c/accessories"`) {
		t.Error("a department that is not current carries aria-current")
	}
}

// TestHeaderMenuOneDepartmentNoHeading: the menu of one department has no heading.
//
// With one department there is nothing to be a list of: no heading, the
// department first, and then the deals page.
func TestHeaderMenuOneDepartmentNoHeading(t *testing.T) {
	t.Parallel()

	for _, items := range [][]frame.NavItem{chromeNav[:1], chromeNav} {
		drawer := drawerOf(t, renderHeader(t, items, true, frame.Page{}))
		if got, want := strings.Contains(drawer, "goen-header__drawerheading"), len(items) > 1; got != want {
			t.Errorf("%d departments: the menu has a heading = %v, want %v", len(items), got, want)
		}
		first := strings.Index(drawer, `href="/c/phones"`)
		deals := strings.Index(drawer, `href="/deals"`)
		if first < 0 || deals < first {
			t.Errorf("%d departments: the first department is at %d and the deals link at %d", len(items), first, deals)
		}
	}
}

// TestHeaderMenuProductCount: the menu prints each department's product count.
func TestHeaderMenuProductCount(t *testing.T) {
	t.Parallel()

	header := renderHeader(t, []frame.NavItem{{Slug: "a", Name: "甲", Href: "/c/a", ProductCount: 12}, {Slug: "b", Name: "乙", Href: "/c/b"}}, false, frame.Page{})
	if !strings.Contains(header, "<small>12</small>") {
		t.Error("the menu does not print a department's count")
	}
	if !strings.Contains(header, `<span class="goen-header__navname">甲</span> <small>12</small>`) {
		t.Error("the menu's department name is not in its own element, so the current-page underline would run under the count")
	}
	if strings.Count(header, "<small>") != 2 {
		t.Error("a department with no products prints a count of 0, or the rail and the menu do not both print the one that has some")
	}
}

// TestFooterGroupsHelpAndDocuments: the footer groups contact with help and the shop's documents together.
//
// The footer's columns: contact beside the help pages, and the shop's own
// documents under their own heading.
func TestFooterGroupsHelpAndDocuments(t *testing.T) {
	t.Parallel()

	_, footer := renderChrome(t, i18n.ZhHant, chromeNav)
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	column := func(title i18n.Key) string {
		at := strings.Index(footer, `aria-label="`+i18n.T(ctx, title)+`"`)
		if at < 0 {
			t.Fatalf("the footer has no %q column", i18n.T(ctx, title))
		}
		col, _, closed := strings.Cut(footer[at:], "</nav>")
		if !closed {
			t.Fatalf("the %q column is never closed", i18n.T(ctx, title))
		}
		return col
	}
	for title, hrefs := range map[i18n.Key][]string{
		i18n.KeyFooterHelp:       {"/contact", "/faq", "/shipping", "/payment", "/returns", "/warranty"},
		i18n.KeyFooterAboutTerms: {"/about", "/terms", "/privacy"},
	} {
		col := column(title)
		if got := strings.Count(col, "<a "); got != len(hrefs) {
			t.Errorf("%s has %d links, want %d", i18n.T(ctx, title), got, len(hrefs))
		}
		for _, h := range hrefs {
			if !strings.Contains(col, `href="`+h+`"`) {
				t.Errorf("%s does not link %s", i18n.T(ctx, title), h)
			}
		}
	}
}

// TestBaseSkipLinkFirstTabStop: the skip link is the first tab stop and names the main landmark.
//
// The first thing a keyboard reaches is the way past the header, and what it
// names exists: a skip link to an id nothing carries does nothing.
func TestBaseSkipLinkFirstTabStop(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	if err := frame.Base(frame.Page{Title: "t"}).Render(templ.WithChildren(ctx, templ.NopComponent), &b); err != nil {
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

// TestBaseAdminStylesheetLinks: only the back office links its stylesheet.
//
// The back office has its own sheet and the storefront's is not loaded there,
// so a storefront restyle cannot reach /admin; the shop never pays for it.
func TestBaseAdminStylesheetLinks(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	ctx = templ.WithChildren(ctx, templ.NopComponent)
	link := func(name string) string {
		return `<link rel="stylesheet" href="` + assets.URL(name) + `">`
	}
	tests := []struct {
		name string
		page templ.Component
		want []string
		not  []string
	}{
		{
			name: "storefront",
			page: frame.Base(frame.Page{Title: "t"}),
			want: []string{link(assets.BaseCSS), link(assets.AppCSS)},
			not:  []string{assets.AdminCSS},
		},
		{
			name: "back office",
			page: frame.Admin(frame.Page{Title: "t"}, "orders"),
			want: []string{link(assets.BaseCSS), link(assets.AdminCSS)},
			not:  []string{assets.AppCSS},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var b strings.Builder
			if err := tt.page.Render(ctx, &b); err != nil {
				t.Fatalf("render: %v", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(b.String(), want) {
					t.Errorf("the head does not contain %s", want)
				}
			}
			for _, not := range tt.not {
				if strings.Contains(b.String(), not) {
					t.Errorf("the head links %s", not)
				}
			}
		})
	}
}

// TestHeaderFooterDepartmentsLabel: the department navigation is named departments.
func TestHeaderFooterDepartmentsLabel(t *testing.T) {
	t.Parallel()

	for locale, name := range map[i18n.Locale]string{i18n.ZhHant: "館別", i18n.En: "Departments"} {
		header, footer := renderChrome(t, locale, chromeNav)
		if got := strings.Count(header, `aria-label="`+name+`"`); got != 2 {
			t.Errorf("%s: the header names %d navigations %q, want 2 (drawer and bar)", locale, got, name)
		}
		if !strings.Contains(footer, `aria-label="`+name+`"`) ||
			!strings.Contains(footer, `<span class="goen-footer__heading">`+name+`</span>`) {
			t.Errorf("%s: the footer's department links are not named %q", locale, name)
		}
	}
}

// TestHeaderCartCount: the cart says 購物車, its count and its subtotal, and its name says them in a sentence.
//
// The count is the lasting answer to an add to cart, so it is shown as the
// word and a number after it, never alone and never once the cart is empty;
// the subtotal follows where there is one, in the cart page's own amount. The
// link's name is the sentence, which the numbers by themselves do not make,
// held in the link: an aria-label would have to contain the visible text word
// for word (WCAG 2.5.3), and the sentence does not.
func TestHeaderCartCount(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		cart frame.CartSummary
		name string
		want string
	}{
		{frame.CartSummary{}, "購物車，目前是空的", ""},
		{frame.CartSummary{Units: 1}, "購物車，1 件商品", `購物車 <span class="goen-header__count">1</span>`},
		{frame.CartSummary{Units: 12, SubtotalCents: 190700}, "購物車，12 件商品，小計 NT$1,907",
			`購物車 <span class="goen-header__count">12</span> <span class="goen-header__subtotal">NT$1,907</span>`},
	} {
		ctx := frame.WithCart(i18n.WithLocale(t.Context(), i18n.ZhHant), tc.cart)
		var b strings.Builder
		if err := frame.Header(frame.Page{}).Render(ctx, &b); err != nil {
			t.Fatalf("render header: %v", err)
		}
		_, cart, _ := strings.Cut(b.String(), `id="cart-link"`)
		cart, _, _ = strings.Cut(cart, "</a>")
		if strings.Contains(cart, "aria-label") {
			t.Errorf("%+v: the cart link is named by an aria-label: %s", tc.cart, cart)
		}
		if !strings.Contains(cart, `<span class="goen-header__cartshown" aria-hidden="true">購物車`) {
			t.Errorf("%+v: the cart link is %s, want the word 購物車 shown and hidden from a screen reader", tc.cart, cart)
		}
		if !strings.Contains(cart, `<span class="goen-sr-only">`+tc.name+`</span>`) {
			t.Errorf("%+v: the cart link is %s, want the sentence %q as its name", tc.cart, cart, tc.name)
		}
		if strings.Contains(cart, "<svg") || strings.Contains(cart, "badge") {
			t.Errorf("%+v: the cart link draws a glyph or a badge: %s", tc.cart, cart)
		}
		if tc.want == "" {
			if strings.Contains(cart, "goen-header__count") || strings.Contains(cart, "goen-header__subtotal") {
				t.Errorf("%+v: an empty cart prints a count or a subtotal", tc.cart)
			}
			continue
		}
		if !strings.Contains(cart, tc.want) {
			t.Errorf("%+v: the cart link is %s, want %s", tc.cart, cart, tc.want)
		}
		if tc.cart.SubtotalCents == 0 && strings.Contains(cart, "goen-header__subtotal") {
			t.Errorf("%+v: a cart with nothing sellable prints a subtotal", tc.cart)
		}
	}
}

// TestHeaderDeliveryWord: the rail says what delivery costs this visitor, and nothing it cannot stand behind.
//
// A cart with something in it says what its own page says: how much more makes
// every method free, that delivery is free, and where it is still charged; a
// cart whose methods disagree says nothing. Any other visitor reads the shop's
// stated threshold, and none where a method never turns free.
func TestHeaderDeliveryWord(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		cart     frame.CartSummary
		freeOver int64
		want     string
	}{
		{"no cart", frame.CartSummary{}, 300000, `<a class="goen-rail__delivery" href="/shipping">滿 NT$3,000 免運</a>`},
		{"no threshold", frame.CartSummary{}, 0, ""},
		{"short", frame.CartSummary{Units: 2, SubtotalCents: 190700, Delivery: frame.CartDelivery{ShortCents: 109300}}, 300000,
			`<a class="goen-rail__delivery" href="/cart">免運還差 NT$1,093</a>`},
		{"free", frame.CartSummary{Units: 2, SubtotalCents: 310000, Delivery: frame.CartDelivery{Free: true}}, 300000,
			`<a class="goen-rail__delivery" href="/cart">已享免運</a>`},
		{"free except", frame.CartSummary{Units: 2, SubtotalCents: 310000, Delivery: frame.CartDelivery{Free: true, Zones: []string{"澎湖", "金門"}}}, 300000,
			`<a class="goen-rail__delivery" href="/cart">已享免運（澎湖、金門另計）</a>`},
		{"methods disagree", frame.CartSummary{Units: 2, SubtotalCents: 190700}, 300000, ""},
	} {
		ctx := frame.WithFreeDeliveryOver(frame.WithCart(i18n.WithLocale(t.Context(), i18n.ZhHant), tc.cart), tc.freeOver)
		var b strings.Builder
		if err := frame.Header(frame.Page{}).Render(ctx, &b); err != nil {
			t.Fatalf("render header: %v", err)
		}
		got := regexp.MustCompile(`<a class="goen-rail__delivery"[^>]*>[^<]*</a>`).FindString(b.String())
		if got != tc.want {
			t.Errorf("%s: the rail says %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestHeaderMenuDialog: the menu's button opens a modal dialog it names.
//
// The drawer is a <dialog> so the browser traps focus in it, closes it on
// Escape and returns focus to the button; the button says it opens a dialog
// and which one, and the dialog has a name of its own.
func TestHeaderMenuDialog(t *testing.T) {
	t.Parallel()

	header, _ := renderChrome(t, i18n.ZhHant, chromeNav)
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	if !strings.Contains(header, `<dialog class="goen-menu" id="site-menu" aria-label="`+i18n.T(ctx, i18n.KeyMenu)+`">`) {
		t.Error("the menu is not a named dialog with the id its button controls")
	}
	if strings.Count(header, `aria-controls="site-menu"`) != 1 {
		t.Error("no single button controls the menu dialog")
	}
	if strings.Contains(header, "<details class=\"goen-header__menu\"") || strings.Contains(header, "data-menu>") {
		t.Error("the menu is still a details disclosure")
	}
}
