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
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/returnpage"
	"github.com/koopa0/goen/internal/user"
)

func TestRefusedCancellationLinksBackToTheOrderAndContact(t *testing.T) {
	vid := freshVariant(t, "cancel-recovery")
	orderID := heldOrder(t, vid, -time.Hour, true)
	number := numberOf(t, orderID)
	appPool := storeRolePool(t)
	h := cart.NewHandler(cart.NewStore(appPool), orderaccess.NewStore(appPool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, nil)
	cookie := placedCookie(t, number)
	before := stockOf(t, vid)
	for _, locale := range i18n.Locales() {
		r := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), locale), http.MethodPost, "/orders/"+number+"/cancel", http.NoBody)
		r.SetPathValue("number", number)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.CancelOrder(w, r)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("refused cancellation status=%d, want 422", w.Code)
		}
		actions := orderRecoveryActions(t, w.Body.String(), "/orders/"+number)
		if !strings.Contains(actions, `href="/contact"`) || strings.Contains(actions, "/contact?") {
			t.Error("refused cancellation has no contact link without prefilling")
		}
		if !strings.Contains(actions, i18n.T(r.Context(), i18n.KeyBackToOrder)) {
			t.Error("refused cancellation has no back-to-order label")
		}
	}
	if got := stockOf(t, vid); got != before {
		t.Errorf("refused cancellation changed stock from %d to %d", before, got)
	}
}

func TestSignedInGuestOrderAccessOffersLookupWithoutSignIn(t *testing.T) {
	number := numberOf(t, heldOrder(t, freshVariant(t, "guest-recovery"), -time.Hour, false))
	var customerID uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO users (email) VALUES ($1) RETURNING id`, "recovery-"+uuid.NewString()+"@example.com").Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	appPool := storeRolePool(t)
	access := orderaccess.NewStore(appPool, false)
	log := slog.New(slog.DiscardHandler)
	basket := cart.NewHandler(cart.NewStore(appPool), access, log, false, testLimiter(), nil, nil)
	gateway, err := payment.NewGateway("", "", "http://goen.example")
	if err != nil {
		t.Fatal(err)
	}
	pay := payment.NewHandler(payment.NewStore(appPool), gateway, access, log)
	returns := returnpage.NewHandler(returnpage.NewStore(appPool), access, log)
	for _, locale := range i18n.Locales() {
		for _, route := range []struct {
			path    string
			method  string
			handler http.HandlerFunc
		}{
			{"", http.MethodGet, basket.OrderPage},
			{"/pay", http.MethodGet, pay.Page},
			{"/return", http.MethodGet, returns.Page},
			{"/cancel", http.MethodPost, basket.CancelOrder},
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
