package layouts

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
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
		declared := slices.Clone(g.declared)
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

	const pages = "../pages"
	files, err := os.ReadDir(pages)
	if err != nil {
		t.Fatalf("list the pages package: %v", err)
	}
	call := regexp.MustCompile(`layouts\.Admin\(p, "([a-z]+)"\)`)

	var out []string
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".templ") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(pages, f.Name()))
		if readErr != nil {
			t.Fatalf("read %s: %v", f.Name(), readErr)
		}
		for _, m := range call.FindAllStringSubmatch(string(body), -1) {
			if !slices.Contains(out, m[1]) {
				out = append(out, m[1])
			}
		}
	}
	if len(out) < 20 {
		t.Fatalf("only %d nav callers found; the parser is not reading the templates", len(out))
	}
	return out
}
