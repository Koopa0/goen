package staff

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/user"
)

// TestNoticesCoverRedirects: every redirect the staff forms make carries a message.
func TestNoticesCoverRedirects(t *testing.T) {
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

// TestHandler_RoutesAdminOnly: only an admin reaches the roster.
func TestHandler_RoutesAdminOnly(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	NewHandler(&Store{}, log, false).Routes(mux, admin.NewControl(log, nil))
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/admin/staff"},
		{http.MethodPost, "/admin/staff"},
		{http.MethodPost, "/admin/staff/revoke"},
		{http.MethodPost, "/admin/staff/factor"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			t.Parallel()
			ctx := user.NewContext(t.Context(), user.User{ID: uuid.NewString(), Role: user.RoleStaff})
			req := httptest.NewRequestWithContext(ctx, route.method, route.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusNotFound {
				t.Errorf("a staff member who is not an admin got %d, want the 404 everyone else gets", w.Code)
			}
		})
	}
}

// TestDoneNoticesAreDistinct: no two actions share a sentence, so a save says
// which change it was.
func TestDoneNoticesAreDistinct(t *testing.T) {
	t.Parallel()
	seen := map[i18n.Key]string{}
	for name, e := range notices {
		if e.Outcome != widget.OutcomeDone {
			continue
		}
		if first, dup := seen[e.Key]; dup {
			t.Errorf("?%s=1 and ?%s=1 both say %q", first, name, e.Key)
		}
		seen[e.Key] = name
	}
}
