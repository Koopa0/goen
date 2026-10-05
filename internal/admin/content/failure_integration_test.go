//go:build integration

package content_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/content"
)

// TestContentTogglesAnswerAFailureAsAServerError: a database that cannot be
// reached decided nothing, and answering 404 or "refused" says otherwise.
func TestContentTogglesAnswerAFailureAsAServerError(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	closed := admintest.NamedPool(t, pool, "content_closed_"+uuid.NewString()[:8])
	closed.Close()
	h := handlerOver(content.NewStore(closed))
	for _, tt := range []struct {
		name  string
		form  url.Values
		serve http.HandlerFunc
	}{
		{name: "delete faq entry", form: url.Values{"action": {"delete"}}, serve: h.EditFAQ},
		{name: "toggle promo banner", form: url.Values{"active": {"1"}}, serve: h.SetBannerActive},
		{name: "toggle hero slide", form: url.Values{"active": {"true"}}, serve: h.SetHeroActive},
		{name: "promote hero slide", form: url.Values{}, serve: h.PromoteHero},
	} {
		id := uuid.NewString()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/"+id, strings.NewReader(tt.form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("id", id)
		res := httptest.NewRecorder()
		tt.serve(res, req)
		if res.Code != http.StatusInternalServerError {
			t.Errorf("%s on a closed pool = %d %s, want 500", tt.name, res.Code, res.Header().Get("Location"))
		}
	}
}
