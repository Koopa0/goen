//go:build integration

package faq_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/faq"
)

// TestHandler_EditFAQClosedDatabase: a FAQ row write answers a failure as a server error. A database that cannot be
// reached decided nothing, and answering 404 or "refused" says otherwise.
func TestHandler_EditFAQClosedDatabase(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	closed := admintest.NamedPool(t, pool, "faq_closed_"+uuid.NewString()[:8])
	closed.Close()
	h := handlerOver(faq.NewStore(closed))
	id := uuid.NewString()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/"+id, strings.NewReader(url.Values{"action": {"delete"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", id)
	res := httptest.NewRecorder()
	h.EditFAQ(res, req)
	if res.Code != http.StatusInternalServerError {
		t.Errorf("delete faq entry on a closed pool = %d %s, want 500", res.Code, res.Header().Get("Location"))
	}
}
