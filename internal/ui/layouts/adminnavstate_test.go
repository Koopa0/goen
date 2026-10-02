package layouts_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// The bar carries a language menu that is a <details> too; only the nav's own are read.
var detailsTag = regexp.MustCompile(`<details class="goen-(?:disclosure|admin__navgroup)[^>]*>`)

// adminDetails renders the back office shell for one screen and returns the
// opening tag of every <details> in its nav: the nav itself first, then its five
// groups in order.
func adminDetails(t *testing.T, current string) []string {
	t.Helper()
	ctx := templ.WithChildren(i18n.WithLocale(t.Context(), i18n.ZhHant), templ.Raw("<p>work</p>"))
	var b strings.Builder
	if err := layouts.Admin(layouts.Page{Title: "t"}, current).Render(ctx, &b); err != nil {
		t.Fatalf("render %s: %v", current, err)
	}
	tags := detailsTag.FindAllString(b.String(), -1)
	if len(tags) != 6 {
		t.Fatalf("%s: %d <details> in the shell, want the nav and its five groups", current, len(tags))
	}
	return tags
}

func isOpen(tag string) bool { return strings.Contains(tag, " open") }

// A phone gets the nav behind its toggle, so the first screen of work is not
// 23 links; the desk stylesheet is what shows it as a rail.
func TestTheAdminNavStartsClosed(t *testing.T) {
	t.Parallel()
	for _, screen := range []string{"dashboard", "orders", "stock"} {
		if tags := adminDetails(t, screen); isOpen(tags[0]) {
			t.Errorf("%s: the nav renders open, so a phone starts with the whole menu on screen: %s", screen, tags[0])
		}
	}
}

// The dashboard is where a shift starts and orders and returns are what it
// starts with, so their group is open there without a click.
func TestTheQueuesGroupIsOpenOnTheDashboard(t *testing.T) {
	t.Parallel()
	groups := adminDetails(t, "dashboard")[1:]
	if !isOpen(groups[0]) {
		t.Errorf("the queues group is closed on the dashboard: %s", groups[0])
	}
	for i, g := range groups[1:] {
		if isOpen(g) {
			t.Errorf("group %d is open on the dashboard, only the queues group should be", i+1)
		}
	}
}

// Opening one group must not shut another: a shared name is what makes the
// browser do that, and a staff member moving between two groups would lose the
// first each time.
func TestAdminNavGroupsDoNotCloseEachOther(t *testing.T) {
	t.Parallel()
	for _, tag := range adminDetails(t, "stock")[1:] {
		if strings.Contains(tag, "name=") {
			t.Errorf("a nav group has a name, which makes the groups exclusive: %s", tag)
		}
	}
}

// Reading a screen opens the group it belongs to, and only that one.
func TestTheGroupHoldingTheCurrentScreenIsTheOneOpen(t *testing.T) {
	t.Parallel()
	groups := adminDetails(t, "stock")[1:]
	for i, g := range groups {
		if want := i == 1; isOpen(g) != want {
			t.Errorf("on stock, group %d open=%t, want %t", i, isOpen(g), want)
		}
	}
}

func TestAdminFooterNamesOnlyTheShop(t *testing.T) {
	t.Parallel()
	ctx := templ.WithChildren(i18n.WithLocale(t.Context(), i18n.ZhHant), templ.NopComponent)
	var b strings.Builder
	if err := layouts.Admin(layouts.Page{Title: "t"}, "").Render(ctx, &b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "© 2026 goen</span>") {
		t.Error("the back office footer is not the bare name")
	}
}
