//go:build integration

package products_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/products"
)

// TestPublishAnswersEachOutcome: publishing a product with nothing to sell is
// refused at COMMIT by the deferred products_active_has_variant, which is a
// rule staff can act on; a database that cannot be reached is not.
func TestPublishAnswersEachOutcome(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	empty := admintest.DraftProduct(t, ctx, pool, s)
	closed := admintest.NamedPool(t, pool, "publish_closed_"+uuid.NewString()[:8])
	closed.Close()

	publish := func(p *products.Handler, slug string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/products/"+slug+"/status",
			strings.NewReader(url.Values{"status": {"active"}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("slug", slug)
		res := httptest.NewRecorder()
		p.Publish(res, req)
		return res
	}

	if res := publish(admintest.ProductDesk(pool, s), empty); res.Code != http.StatusSeeOther ||
		res.Header().Get("Location") != "/admin/products/"+empty+"?refused=1" {
		t.Errorf("Publish(%s with no variant) = %d %q, want 303 to ?refused=1", empty, res.Code, res.Header().Get("Location"))
	}
	if res := publish(admintest.ProductDesk(closed, products.NewStore(closed)), empty); res.Code != http.StatusInternalServerError {
		t.Errorf("Publish(%s) on a closed pool = %d %q, want 500", empty, res.Code, res.Header().Get("Location"))
	}
	if res := publish(admintest.ProductDesk(pool, s), "no-such-"+uuid.NewString()[:8]); res.Code != http.StatusNotFound {
		t.Errorf("Publish(missing slug) = %d %q, want 404", res.Code, res.Header().Get("Location"))
	}
}
