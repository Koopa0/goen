package layouts_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/web"
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
		var other i18n.Locale
		for _, l := range i18n.Locales() {
			if l != locale {
				other = l
			}
		}
		if !strings.Contains(header, `lang="`+other.Tag()+`">`+other.Short()+`</span>`) {
			t.Errorf("%s: the menu button does not show %q, the language it switches to", locale, other.Short())
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
	if !strings.Contains(footer, `href="/c/phones"`) {
		t.Error("the footer's department column does not link the departments")
	}
}

func TestDepartmentPanelUsesOnlyItsConfiguredPhotograph(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, tt := range []struct {
			name string
			slug string
			url  string
		}{
			{"new department upload", "new-department", "/media/" + strings.Repeat("a", 64)},
			{"replaced seeded photograph", "books-stationery", "/media/" + strings.Repeat("b", 64)},
			{"cleared seeded photograph", "books-stationery", ""},
		} {
			t.Run(string(locale)+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				item := layouts.NavItem{
					Slug: tt.slug, Name: "Department", Href: "/c/" + tt.slug,
					PhotoURL: tt.url,
					Children: []layouts.NavItem{{Slug: "child", Name: "Child", Href: "/c/child"}},
				}
				if tt.url != "" {
					item.PhotoSrcset = tt.url + "/400 400w, " + tt.url + " 600w"
				}
				header, _ := renderChrome(t, locale, []layouts.NavItem{item, {Slug: "other", Name: "Other", Href: "/c/other"}})
				photos := regexp.MustCompile(`<img class="goen-dept__photo"[^>]*>`).FindAllString(header, -1)
				if tt.url == "" {
					if len(photos) != 0 {
						t.Fatalf("cleared department renders photographs: %v", photos)
					}
					return
				}
				if len(photos) != 1 {
					t.Fatalf("department renders %d photographs, want 1", len(photos))
				}
				for _, want := range []string{`src="` + item.PhotoURL + `"`, `srcset="` + item.PhotoSrcset + `"`, `alt=""`} {
					if !strings.Contains(photos[0], want) {
						t.Errorf("department photograph %s omits %s", photos[0], want)
					}
				}
			})
		}
	}
}

func renderHeader(t *testing.T, items []layouts.NavItem, deals bool, page layouts.Page) string {
	t.Helper()
	ctx := layouts.WithDeals(layouts.WithTopNav(i18n.WithLocale(t.Context(), i18n.ZhHant), items), deals)
	var b strings.Builder
	if err := layouts.Header(page).Render(ctx, &b); err != nil {
		t.Fatalf("render header: %v", err)
	}
	return b.String()
}

// 優惠 is offered when the deals page has something to buy and not otherwise.
func TestTheHeaderOffersDealsOnlyWhenThereIsSomethingToBuy(t *testing.T) {
	t.Parallel()

	for _, deals := range []bool{true, false} {
		header := renderHeader(t, chromeNav, deals, layouts.Page{})
		if got := strings.Contains(header, `href="/deals"`); got != deals {
			t.Errorf("deals=%v: the header links /deals = %v", deals, got)
		}
		if got := strings.Count(header, `href="/deals"`); deals && got != 2 {
			t.Errorf("deals=%v: /deals is linked %d times, want once in the row and once in the menu", deals, got)
		}
	}
}

func TestTheHeaderMarksTheCurrentDealsPage(t *testing.T) {
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
					ctx := layouts.WithDeals(layouts.WithTopNav(i18n.WithLocale(t.Context(), locale), chromeNav), tt.deals)
					ctx = web.WithRequestPath(ctx, tt.path)
					if origin != "" {
						ctx = layouts.WithRequestPath(layouts.WithSiteOrigin(ctx, origin), "/")
					}
					var b strings.Builder
					if err := layouts.Header(layouts.Page{Nav: tt.nav}).Render(ctx, &b); err != nil {
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

// A shop with one department and nothing on offer has nothing to put in a
// second row.
func TestTheDepartmentRowIsAbsentForOneDepartmentAndNoDeals(t *testing.T) {
	t.Parallel()

	one := chromeNav[:1]
	tests := []struct {
		name  string
		items []layouts.NavItem
		deals bool
		want  bool
	}{
		{name: "one department, no deals", items: one, deals: false, want: false},
		{name: "one department, deals", items: one, deals: true, want: true},
		{name: "two departments, no deals", items: chromeNav, deals: false, want: true},
	}
	for _, tt := range tests {
		header := renderHeader(t, tt.items, tt.deals, layouts.Page{})
		if got := strings.Contains(header, `class="goen-header__nav"`); got != tt.want {
			t.Errorf("%s: the department row is present = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestTheCurrentDepartmentIsMarked(t *testing.T) {
	t.Parallel()

	header := renderHeader(t, chromeNav, false, layouts.Page{Nav: "phones"})
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

// With one department there is nothing to be a list of: no heading, the
// department first, and then the deals page.
func TestTheMenuOfOneDepartmentHasNoHeading(t *testing.T) {
	t.Parallel()

	for _, items := range [][]layouts.NavItem{chromeNav[:1], chromeNav} {
		header := renderHeader(t, items, true, layouts.Page{})
		start := strings.Index(header, `class="goen-header__drawer"`)
		drawer := header[start:strings.Index(header, `class="goen-header__brand"`)]
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

func TestTheMenuPrintsEachDepartmentsProductCount(t *testing.T) {
	t.Parallel()

	header := renderHeader(t, []layouts.NavItem{{Slug: "a", Name: "甲", Href: "/c/a", ProductCount: 12}, {Slug: "b", Name: "乙", Href: "/c/b"}}, false, layouts.Page{})
	if !strings.Contains(header, "<small>12</small>") {
		t.Error("the menu does not print a department's count")
	}
	if !strings.Contains(header, `<span class="goen-header__navname">甲</span> <small>12</small>`) {
		t.Error("the menu's department name is not in its own element, so the current-page underline would run under the count")
	}
	if strings.Count(header, "<small>") != 1 {
		t.Error("a department with no products prints a count of 0")
	}
}

// The footer's columns: contact beside the help pages, and the shop's own
// documents under their own heading.
func TestTheFooterGroupsContactWithHelpAndTheShopsDocumentsTogether(t *testing.T) {
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

// The back office has its own sheet and the storefront's is not loaded there,
// so a storefront restyle cannot reach /admin; the shop never pays for it.
func TestOnlyTheBackOfficeLinksItsStylesheet(t *testing.T) {
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
			page: layouts.Base(layouts.Page{Title: "t"}),
			want: []string{link(assets.BaseCSS), link(assets.AppCSS)},
			not:  []string{assets.AdminCSS},
		},
		{
			name: "back office",
			page: layouts.Admin(layouts.Page{Title: "t"}, "orders"),
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

func TestTheDepartmentNavigationIsNamedDepartments(t *testing.T) {
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

func TestTheCartCountSitsBesideTheBagAndOnlyWhenThereIsSomethingInIt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		count int
		want  string
	}{
		{0, ""},
		{1, `<span class="ui-badge--count" aria-hidden="true">1</span>`},
		{12, `<span class="ui-badge--count" aria-hidden="true">12</span>`},
	} {
		ctx := web.WithCartCount(i18n.WithLocale(t.Context(), i18n.ZhHant), tc.count)
		var b strings.Builder
		if err := layouts.Header(layouts.Page{}).Render(ctx, &b); err != nil {
			t.Fatalf("render header: %v", err)
		}
		_, cart, _ := strings.Cut(b.String(), `id="cart-link"`)
		cart, _, _ = strings.Cut(cart, "</a>")
		if !strings.Contains(cart, `d="M9 10.5V7a3 3 0 0 1 6 0v3.5"`) {
			t.Errorf("count %d: the cart link does not draw a bag", tc.count)
		}
		if tc.want == "" {
			if strings.Contains(cart, "ui-badge--count") {
				t.Errorf("count %d: an empty cart prints a count", tc.count)
			}
			continue
		}
		if !strings.Contains(cart, tc.want) {
			t.Errorf("count %d: the cart link lacks %s", tc.count, tc.want)
		}
	}
}
