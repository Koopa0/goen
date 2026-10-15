package staff

import (
	"fmt"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/user"
)

func renderStaff(t *testing.T, locale i18n.Locale, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), locale), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func TestStaffRowPrintsAnEmailOnce(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		row   StaffRow
		count int
	}{
		"no name":   {StaffRow{ID: "1", Email: "nameless@example.com", Role: user.RoleStaff}, 1},
		"with name": {StaffRow{ID: "2", Email: "named@example.com", Role: user.RoleStaff, Name: "Mei"}, 1},
	} {
		body := renderStaff(t, i18n.En, Staff(layouts.Page{Title: "Staff"}, StaffView{Rows: []StaffRow{tc.row}}))
		if got := strings.Count(body, tc.row.Email); got != tc.count {
			t.Errorf("%s: %q appears %d times in the staff list, want %d", name, tc.row.Email, got, tc.count)
		}
	}
}

// badgeClasses returns the class attribute of every badge whose text contains word.
func badgeClasses(t *testing.T, page, word string) []string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out []string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			for _, a := range n.Attr {
				if a.Key == "class" && strings.HasPrefix(a.Val, "goen-badge") && strings.Contains(textOf(n), word) {
					out = append(out, a.Val)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

func textOf(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textOf(c))
	}
	return strings.TrimSpace(b.String())
}

func wantBadge(t *testing.T, page, word, class string) {
	t.Helper()
	got := badgeClasses(t, page, word)
	if len(got) == 0 {
		t.Errorf("no badge says %q", word)
		return
	}
	for _, c := range got {
		if c != class {
			t.Errorf("badge %q has class %q, want %q", word, c, class)
		}
	}
}

func TestStaffEnrollmentBadgesAreDoneOrNeedYou(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := StaffView{Rows: []StaffRow{
		{ID: "on", Role: user.RoleStaff, Enrolled: true},
		{ID: "off", Role: user.RoleStaff},
	}}
	page := renderStaff(t, i18n.ZhHant, Staff(layouts.Page{}, view))
	wantBadge(t, page, i18n.T(ctx, i18n.KeyAdminTOTPOn), "goen-badge goen-badge--done")
	wantBadge(t, page, i18n.T(ctx, i18n.KeyAdminTOTPOff), "goen-badge goen-badge--warn")
}

func TestStaffCountSelectsSingularInTheRenderedPage(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, n := range []int32{1, 2} {
			wantStaff := fmt.Sprintf("%d accounts have not enrolled", n)
			if n == 1 {
				wantStaff = "1 account has not enrolled"
			}
			if locale == i18n.ZhHant {
				wantStaff = fmt.Sprintf("還有 %d 個帳號尚未設定", n)
			}
			rows := make([]StaffRow, n)
			for i := range rows {
				rows[i] = StaffRow{ID: fmt.Sprintf("staff-%d", i), Email: "staff@example.com", Role: user.RoleStaff}
			}
			staff := renderStaff(t, locale, Staff(layouts.Page{}, StaffView{Rows: rows}))
			if !strings.Contains(staff, wantStaff) {
				t.Errorf("Staff(%s, %d) does not contain %q", locale, n, wantStaff)
			}
		}
	}
}
