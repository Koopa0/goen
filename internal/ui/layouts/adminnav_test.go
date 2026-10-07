package layouts

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestEveryAdminNavGroupOpensOnItsOwnScreens holds the two halves of one group
// together. A <details> names the screens it holds so the server can render it
// `open` on arrival, and the links it holds are the next lines of the same
// file — two lists of the same fact, three lines apart.
//
// Apart, they fail silently and only for the person who is already lost: a link
// added to a group whose list forgot it lands a staff member on a page whose
// group is CLOSED, with no error anywhere and nothing visible to anybody who
// arrived by any other route.
func TestEveryAdminNavGroupOpensOnItsOwnScreens(t *testing.T) {
	t.Parallel()

	groups, standalone := adminNavSource(t)
	if len(groups) < 4 {
		t.Fatalf("only %d nav groups found; the parser is not reading admin.templ", len(groups))
	}

	for _, g := range groups {
		// A standalone screen may open a group it is not in: the dashboard opens
		// the queues group, because that is where a shift starts. Everything else
		// a group declares still has to be one of its own links.
		declared := slices.DeleteFunc(slices.Clone(g.declared), func(screen string) bool {
			return slices.Contains(standalone, screen)
		})
		linked := slices.Clone(g.linked)
		slices.Sort(declared)
		slices.Sort(linked)
		if !slices.Equal(declared, linked) {
			t.Errorf("the %q group opens for %v and links to %v — a screen in one "+
				"list and not the other is a screen whose group is shut when it "+
				"is the one being read", g.label, declared, linked)
		}
	}

	// And every screen the back office renders a nav for belongs to a group, or
	// it is one of the standalone items. A new screen wired into no group opens
	// nothing, which is the same failure one level up.
	held := map[string]string{}
	for _, g := range groups {
		for _, screen := range g.declared {
			if other, dup := held[screen]; dup {
				t.Errorf("%q is held by both %q and %q; two groups open for one screen",
					screen, other, g.label)
			}
			held[screen] = g.label
		}
	}
	for _, screen := range adminNavCallers(t) {
		if _, ok := held[screen]; ok {
			continue
		}
		if slices.Contains(standalone, screen) {
			continue
		}
		t.Errorf("adminNav(%q) is rendered by a page and named by no group, so the "+
			"nav that page arrives with has every group shut", screen)
	}
}

// adminNavGroup is one <details> in the back-office nav: the screens it says it
// holds, and the screens it actually links to.
type adminNavGroup struct {
	label    string
	declared []string
	linked   []string
}

// adminNavSource reads the groups out of the template, plus the screens marked
// current outside any group — the dashboard, which is nobody's group.
func adminNavSource(t *testing.T) (groups []adminNavGroup, standalone []string) {
	t.Helper()

	body, err := os.ReadFile("admin.templ")
	if err != nil {
		t.Fatalf("read admin.templ: %v", err)
	}
	src := string(body)
	start := strings.Index(src, "templ adminNav(current string) {")
	if start < 0 {
		t.Fatal("adminNav is not in admin.templ; the parser is reading the wrong thing")
	}
	end := strings.Index(src[start:], "\n}\n")
	if end < 0 {
		t.Fatal("adminNav has no end; the parser is reading the wrong thing")
	}
	nav := src[start : start+end]

	holds := regexp.MustCompile(`adminNavHolds\(current,([^)]*)\)`)
	quoted := regexp.MustCompile(`"([a-z]+)"`)
	active := regexp.MustCompile(`current == "([a-z]+)"`)
	label := regexp.MustCompile(`i18n\.(KeyAdminNav[A-Za-z]+)`)

	for _, block := range strings.Split(nav, "<details")[1:] {
		if i := strings.Index(block, "</details>"); i >= 0 {
			block = block[:i]
		}
		args := holds.FindStringSubmatch(block)
		if args == nil {
			t.Errorf("a nav group decides its own open state without adminNavHolds; "+
				"the guard cannot read it: %.60s", block)
			continue
		}
		g := adminNavGroup{label: "?"}
		if name := label.FindStringSubmatch(block); name != nil {
			g.label = name[1]
		}
		for _, m := range quoted.FindAllStringSubmatch(args[1], -1) {
			g.declared = append(g.declared, m[1])
		}
		for _, m := range active.FindAllStringSubmatch(block, -1) {
			g.linked = append(g.linked, m[1])
		}
		groups = append(groups, g)
	}

	// Whatever the nav marks current before the first group is standalone.
	head := nav
	if i := strings.Index(nav, "<details"); i >= 0 {
		head = nav[:i]
	}
	for _, m := range active.FindAllStringSubmatch(head, -1) {
		standalone = append(standalone, m[1])
	}
	return groups, standalone
}

// adminNavCallers is every screen the back office renders the nav for. The
// screen is named where the page enters the shell, because the shell is what
// renders the rail now: a page that names a screen the nav has never heard of
// gets a rail with every group shut, and nothing anywhere says so.
//
// The step-up challenge names no screen — it is reached before the back office
// will answer, so it takes the bar and no rail — and the pattern below does not
// match its empty argument, which is how it stays out of this list.
func adminNavCallers(t *testing.T) []string {
	t.Helper()

	call := regexp.MustCompile(`layouts\.Admin\(p, "([a-z]+)"\)`)

	var out []string
	for _, dir := range []string{"../pages", "../pages/admin"} {
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("list %s: %v", dir, err)
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".templ") {
				continue
			}
			body, readErr := os.ReadFile(filepath.Join(dir, f.Name())) //nolint:gosec // G304: dir is one of the two pages directories above
			if readErr != nil {
				t.Fatalf("read %s: %v", f.Name(), readErr)
			}
			for _, m := range call.FindAllStringSubmatch(string(body), -1) {
				if !slices.Contains(out, m[1]) {
					out = append(out, m[1])
				}
			}
		}
	}
	if len(out) < 20 {
		t.Fatalf("only %d nav callers found; the parser is not reading the templates", len(out))
	}
	return out
}

func TestBackgroundNavigationDistinguishesKnownCountsFromFailedReads(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, tt := range []struct {
			name   string
			count  int64
			known  bool
			zh, en string
		}{
			{"none", 0, true, "0 件要處理", "0 tasks need attention"},
			{"one", 1, true, "1 件要處理", "1 task needs attention"},
			{"many", 112, true, "112 件要處理", "112 tasks need attention"},
			{"unknown", 0, false, "待辦數無法查詢", "Task count unavailable"},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				ctx := WithHealthTaskCount(i18n.WithLocale(t.Context(), locale), tt.count, tt.known)
				var body bytes.Buffer
				if err := Admin(Page{}, "products").Render(ctx, &body); err != nil {
					t.Fatal(err)
				}
				link := regexp.MustCompile(`<a[^>]*href="/admin/health"[^>]*>(.*?)</a>`).FindStringSubmatch(body.String())
				if len(link) != 2 {
					t.Fatal("background navigation link is missing")
				}
				want := tt.en
				if locale == i18n.ZhHant {
					want = tt.zh
				}
				if !strings.Contains(link[1], want) {
					t.Errorf("background link=%q, want %q", link[1], want)
				}
			})
		}
	}
}
