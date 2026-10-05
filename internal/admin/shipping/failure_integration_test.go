//go:build integration

package shipping_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/shipping"
)

// TestShippingTogglesAnswerAFailureAsAServerError: a database that cannot be
// reached decided nothing, and answering 404 says the method or zone is gone.
func TestShippingTogglesAnswerAFailureAsAServerError(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	closed := admintest.NamedPool(t, pool, "shipping_closed_"+uuid.NewString()[:8])
	closed.Close()
	h := handlerOver(shipping.NewStore(closed))
	for _, tt := range []struct {
		name  string
		form  url.Values
		serve http.HandlerFunc
	}{
		{name: "toggle shipping method", form: url.Values{"active": {"1"}}, serve: h.SetMethodActive},
		{name: "delete shipping zone", form: url.Values{}, serve: h.DeleteZone},
	} {
		id := uuid.NewString()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/shipping/"+id, strings.NewReader(tt.form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("id", id)
		res := httptest.NewRecorder()
		tt.serve(res, req)
		if res.Code != http.StatusInternalServerError {
			t.Errorf("%s on a closed pool = %d %s, want 500", tt.name, res.Code, res.Header().Get("Location"))
		}
	}
}
