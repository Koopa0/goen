package access_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"testing"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/user"
)

func TestStaffNavigationOnlyOmitsTheAdminOnlyDestination(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			c := access.New(slog.New(slog.DiscardHandler), nil)
			links := map[string][]string{}
			for _, role := range []string{"staff", "admin"} {
				ctx := user.NewContext(i18n.WithLocale(t.Context(), locale), user.User{Role: user.Role(role)})
				// A stale presentation hint must not override the authenticated role.
				ctx = layouts.WithAdmin(ctx, true)
				req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/orders", http.NoBody)
				res := httptest.NewRecorder()
				c.RequireStaff(func(w http.ResponseWriter, r *http.Request) {
					if err := layouts.Admin(layouts.Page{}, "orders").Render(r.Context(), w); err != nil {
						t.Error(err)
					}
				})(res, req)
				if res.Code != http.StatusOK {
					t.Fatalf("%s status=%d", role, res.Code)
				}
				for _, match := range regexp.MustCompile(`href="(/admin[^"#]*)"`).FindAllStringSubmatch(res.Body.String(), -1) {
					links[role] = append(links[role], match[1])
				}
				if got := slices.Contains(links[role], "/admin/staff"); got != (role == "admin") {
					t.Errorf("%s staff-management link=%v", role, got)
				}
			}
			if len(links["staff"]) < 20 {
				t.Fatalf("staff lost ordinary navigation: %v", links["staff"])
			}
			want := slices.DeleteFunc(slices.Clone(links["admin"]), func(href string) bool { return href == "/admin/staff" })
			if !slices.Equal(links["staff"], want) {
				t.Errorf("ordinary navigation differs: staff=%v admin=%v", links["staff"], want)
			}
		})
	}
}
