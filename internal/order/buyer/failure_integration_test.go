//go:build integration

package buyer_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/buyer"
)

// A database failure on the order lookup, the order page or a cancellation
// says that the problem was logged, as the return form does. These pages hold
// no cart, so they must not tell the customer the cart is unavailable.
func TestOrderPagesNameNoCartWhenTheDatabaseFails(t *testing.T) {
	number, _ := shippedOrder(t, 1, 0)
	broken := storeRolePool(t)
	broken.Close()
	h := buyer.NewHandler(buyer.NewStore(broken), access.NewStore(pool, false),
		slog.New(slog.DiscardHandler), testLimiter(), false)

	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		for name, route := range map[string]struct {
			method, path string
			handler      http.HandlerFunc
			form         url.Values
		}{
			"lookup": {http.MethodPost, "/orders/find", h.FindOrder, url.Values{"number": {number}, "email": {"x@example.test"}}},
			"page":   {http.MethodGet, "/orders/" + number, h.OrderPage, nil},
			"cancel": {http.MethodPost, "/orders/" + number + "/cancel", h.CancelOrder, nil},
		} {
			req := httptest.NewRequestWithContext(ctx, route.method, route.path, strings.NewReader(route.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("number", number)
			placedBy(t, req, number)
			res := httptest.NewRecorder()
			route.handler(res, req)
			body := res.Body.String()
			if res.Code != http.StatusInternalServerError {
				t.Fatalf("%s/%s: status = %d, want 500", name, locale, res.Code)
			}
			if !strings.Contains(body, i18n.T(ctx, i18n.KeyLoggedTryAgain)) {
				t.Errorf("%s/%s: the failure page does not say the problem was logged", name, locale)
			}
			if strings.Contains(body, i18n.T(ctx, i18n.KeyCartUnavailable)) {
				t.Errorf("%s/%s: the failure page blames the cart", name, locale)
			}
		}
	}
}
