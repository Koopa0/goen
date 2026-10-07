package access_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
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

func TestHealthCountIsReadOnlyAfterStaffAndSecondFactorChecks(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, role              string
		verified                bool
		factorError, countError error
		status, reads           int
		want                    string
	}{
		{name: "anonymous", status: http.StatusNotFound},
		{name: "customer", role: "customer", status: http.StatusNotFound},
		{name: "unverified", role: "staff", status: http.StatusSeeOther},
		{name: "factor unavailable", role: "staff", factorError: errors.New("factor unavailable"), status: http.StatusInternalServerError},
		{name: "staff", role: "staff", verified: true, status: http.StatusOK, reads: 1, want: "7 tasks need attention"},
		{name: "admin", role: "admin", verified: true, status: http.StatusOK, reads: 1, want: "7 tasks need attention"},
		{name: "zero", role: "staff", verified: true, status: http.StatusOK, reads: 1, want: "0 tasks need attention"},
		{name: "count unavailable", role: "staff", verified: true, countError: errors.New("count unavailable"), status: http.StatusOK, reads: 1, want: "Task count unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reads := 0
			c := access.New(slog.New(slog.DiscardHandler), func(*http.Request) (bool, error) {
				return tt.verified, tt.factorError
			}).WithHealthTaskCount(func(context.Context) (int64, error) {
				reads++
				if tt.name == "zero" {
					return 0, nil
				}
				return 7, tt.countError
			})
			ctx := i18n.WithLocale(t.Context(), i18n.En)
			if tt.role != "" {
				ctx = user.NewContext(ctx, user.User{Role: user.Role(tt.role)})
			}
			ctx = layouts.WithHealthTaskCount(ctx, 99, true)
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/products", http.NoBody)
			res := httptest.NewRecorder()
			c.RequireStaff(func(w http.ResponseWriter, r *http.Request) {
				if err := layouts.Admin(layouts.Page{}, "products").Render(r.Context(), w); err != nil {
					t.Error(err)
				}
			})(res, req)
			if res.Code != tt.status || reads != tt.reads {
				t.Fatalf("status=%d count reads=%d, want %d/%d", res.Code, reads, tt.status, tt.reads)
			}
			if tt.want != "" && !strings.Contains(res.Body.String(), tt.want) {
				t.Errorf("navigation lacks %q", tt.want)
			}
			if strings.Contains(res.Body.String(), "99 tasks need attention") {
				t.Error("stale presentation count overrides the authorized read")
			}
			if tt.countError != nil && strings.Contains(res.Body.String(), "0 tasks need attention") {
				t.Error("unavailable count was presented as zero")
			}
		})
	}
}

func TestSecondFactorRoutesDoNotReadHealthCount(t *testing.T) {
	t.Parallel()
	c := access.New(slog.New(slog.DiscardHandler), nil).WithHealthTaskCount(func(context.Context) (int64, error) {
		t.Fatal("second-factor route read the protected health count")
		return 0, nil
	})
	ctx := user.NewContext(t.Context(), user.User{Role: user.Role("staff")})
	res := httptest.NewRecorder()
	c.StaffOnly(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/verify", http.NoBody))
	if res.Code != http.StatusNoContent {
		t.Errorf("second-factor route status=%d", res.Code)
	}
}
