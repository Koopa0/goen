package layouts_test

import (
	"regexp"
	"strings"
	"testing"

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
	if err := layouts.Footer().Render(ctx, &f); err != nil {
		t.Fatalf("render footer: %v", err)
	}
	return h.String(), f.String()
}

var chromeNav = []layouts.NavItem{
	{Slug: "phones", Name: "手機", Href: "/c/phones"},
	{Slug: "accessories", Name: "周邊配件", Href: "/c/accessories", Children: []layouts.NavItem{
		{Slug: "chargers", Name: "充電與線材", Href: "/c/chargers"},
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

// The language control is in the footer once; a second copy in the header is
// a second thing to find and a second form to keep in step.
func TestTheLanguageSwitchIsInTheFooterOnly(t *testing.T) {
	t.Parallel()

	header, footer := renderChrome(t, i18n.En, chromeNav)
	if strings.Contains(header, "goen-lang") {
		t.Error("the header still carries a language control")
	}
	if got := strings.Count(footer, "goen-lang__item"); got != len(i18n.Locales()) {
		t.Errorf("the footer has %d language buttons, want %d", got, len(i18n.Locales()))
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
