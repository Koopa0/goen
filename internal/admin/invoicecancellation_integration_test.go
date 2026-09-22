//go:build integration

package admin_test

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
)

func TestCancellationHandlerExplainsOutstandingInvoice(t *testing.T) {
	ctx, _ := staffContext(t)
	number := placeUnpaidOrder(t)
	if _, err := pool.Exec(ctx, `INSERT INTO invoice_documents (order_id, kind, number, amount_cents) SELECT id, 'invoice', $2, 10000 FROM orders WHERE order_number = $1`, number, uuid.NewString()[:32]); err != nil {
		t.Fatal(err)
	}
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	h := adminHandlerOver(pool, s)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		local := i18n.WithLocale(ctx, locale)
		form := url.Values{"status": {"cancelled"}}
		req := httptest.NewRequestWithContext(local, http.MethodPost, "/admin/orders/"+number+"/status", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("number", number)
		rec := httptest.NewRecorder()
		h.AdvanceOrder(rec, req)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/orders/"+number+"?cancelinvoice=1" {
			t.Fatalf("cancel refusal = %d %s", rec.Code, rec.Header().Get("Location"))
		}
		get := httptest.NewRequestWithContext(local, http.MethodGet, rec.Header().Get("Location"), nil)
		get.SetPathValue("number", number)
		page := httptest.NewRecorder()
		h.Order(page, get)
		if page.Code != http.StatusOK || !strings.Contains(html.UnescapeString(page.Body.String()), i18n.T(local, i18n.KeyAdminNoticeCancelInvoice)) {
			t.Fatalf("%s missing cancellation guidance: %d", locale, page.Code)
		}
	}
}
