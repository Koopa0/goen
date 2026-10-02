package staff

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/access"
)

func TestEveryRedirectTheStaffFormsMakeCarriesAMessage(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	sent := map[string]bool{}
	for _, m := range regexp.MustCompile(`/admin/staff\?([a-z]+)=1`).FindAllStringSubmatch(string(src), -1) {
		sent[m[1]] = true
	}
	if len(sent) == 0 {
		t.Fatal("no redirect parameter found; the parser stopped matching")
	}
	for name := range sent {
		if _, ok := notices[name]; !ok {
			t.Errorf("?%s=1 carries no message: the page renders nothing after the button", name)
		}
	}
	for name := range notices {
		if !sent[name] {
			t.Errorf("notice %q names a parameter no redirect writes", name)
		}
	}
}

func TestOnlyAnAdminReachesTheRoster(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	NewHandler(&Store{}, log, false).Routes(mux, access.New(log, nil))
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/admin/staff"},
		{http.MethodPost, "/admin/staff"},
		{http.MethodPost, "/admin/staff/revoke"},
		{http.MethodPost, "/admin/staff/factor"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			t.Parallel()
			ctx := account.WithUser(t.Context(), account.User{ID: uuid.NewString(), Role: account.RoleStaff})
			req := httptest.NewRequestWithContext(ctx, route.method, route.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusNotFound {
				t.Errorf("a staff member who is not an admin got %d, want the 404 everyone else gets", w.Code)
			}
		})
	}
}
