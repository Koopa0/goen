//go:build integration

package cart_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/user"
)

// TestOrderRoutesSignedInGuest holds that a signed-in customer who reaches a
// guest order's routes (order, pay, return, cancel, reorder) is offered the
// order lookup, not sign-in.
func TestOrderRoutesSignedInGuest(t *testing.T) {
	number := numberOf(t, heldOrder(t, freshVariant(t, "guest-recovery"), -time.Hour, false))
	var customerID uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO users (email) VALUES ($1) RETURNING id`, "recovery-"+uuid.NewString()+"@example.com").Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	appPool := storeRolePool(t)
	grants := access.NewStore(appPool, false)
	log := slog.New(slog.DiscardHandler)
	basket := cart.NewHandler(cart.NewStore(appPool), grants, log, false, nil)
	gateway, err := payment.NewGateway("", "", "http://goen.example")
	if err != nil {
		t.Fatal(err)
	}
	pay := payment.NewHandler(payment.NewStore(appPool), gateway, grants, log)
	buyerOrders := buyer.NewHandler(buyer.NewStore(appPool), grants, log, testLimiter(), false)
	for _, locale := range i18n.Locales() {
		for _, route := range []struct {
			path    string
			method  string
			handler http.HandlerFunc
		}{
			{"", http.MethodGet, buyerOrders.OrderPage},
			{"/pay", http.MethodGet, pay.Page},
			{"/return", http.MethodGet, buyerOrders.ReturnPage},
			{"/cancel", http.MethodPost, buyerOrders.CancelOrder},
			{"/reorder", http.MethodPost, basket.ReorderItems},
		} {
			ctx := user.NewContext(i18n.WithLocale(t.Context(), locale), user.User{ID: customerID.String()})
			r := httptest.NewRequestWithContext(ctx, route.method, "/orders/"+number+route.path, http.NoBody)
			r.SetPathValue("number", number)
			w := httptest.NewRecorder()
			route.handler(w, r)
			if w.Code != http.StatusNotFound {
				t.Fatalf("%s signed-in guest order: status=%d, want 404", route.path, w.Code)
			}
			actions := orderRecoveryActions(t, w.Body.String(), "/orders/find")
			if strings.Contains(actions, `href="/signin"`) || !strings.Contains(w.Body.String(), i18n.T(ctx, i18n.KeyOrderNotInAccount)) {
				t.Errorf("%s signed-in guest order still offers sign-in or lacks account guidance", route.path)
			}
		}
	}
}

func orderRecoveryActions(t *testing.T, body, primary string) string {
	t.Helper()
	_, actions, ok := strings.Cut(body, `class="notice__actions"`)
	if !ok {
		t.Fatal("missing order recovery actions")
	}
	actions, _, _ = strings.Cut(actions, "</div>")
	_, href, _ := strings.Cut(actions, `href="`)
	href, _, _ = strings.Cut(href, `"`)
	if href != primary {
		t.Errorf("primary recovery href=%q, want %q", href, primary)
	}
	return actions
}
