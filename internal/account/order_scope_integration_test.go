//go:build integration

package account_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web/ratelimit"
)

// TestStore_OverviewOwnerScope: orders are scoped to their owner: the order
// page answers a stranger 404 and the overview never lists another customer's
// order.
func TestStore_OverviewOwnerScope(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	mine := register(t, "mine@example.com")
	theirs := register(t, "theirs@example.com")

	number := placeOrderFor(t, theirs.ID)

	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}), false)

	owner := httptest.NewRequestWithContext(user.NewContext(ctx, theirs), http.MethodGet,
		"/orders/"+number, http.NoBody)
	owner.SetPathValue("number", number)
	ownerOK := httptest.NewRecorder()
	h.OrderPage(ownerOK, owner)
	if ownerOK.Code != http.StatusOK {
		t.Fatalf("the owner cannot see their own order: status %d, want 200", ownerOK.Code)
	}

	stranger := httptest.NewRequestWithContext(user.NewContext(ctx, mine), http.MethodGet,
		"/orders/"+number, http.NoBody)
	stranger.SetPathValue("number", number)
	strangerRes := httptest.NewRecorder()
	h.OrderPage(strangerRes, stranger)
	if strangerRes.Code != http.StatusNotFound {
		t.Errorf("another customer's order gave status %d, want 404", strangerRes.Code)
	}

	missing := httptest.NewRequestWithContext(user.NewContext(ctx, mine), http.MethodGet,
		"/orders/GO-000000-999999", http.NoBody)
	missing.SetPathValue("number", "GO-000000-999999")
	missingRes := httptest.NewRecorder()
	h.OrderPage(missingRes, missing)
	if missingRes.Code != http.StatusNotFound {
		t.Errorf("a nonexistent order gave status %d, want 404", missingRes.Code)
	}

	view, err := s.Overview(ctx, mine)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	for _, o := range view.Orders {
		if o.Number == number {
			t.Error("another customer's order is listed in this account's history")
		}
	}
}
