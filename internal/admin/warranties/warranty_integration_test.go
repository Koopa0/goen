//go:build integration

package warranties_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/warranties"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/order/warranty"
)

// TestStore_WarrantiesSearch: the shop can find a warranty the customer registered, by serial or by order number.
func TestStore_WarrantiesSearch(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	serial, number := registeredWarranty(t, "SN-"+strings.ToUpper(uuid.NewString()[:8]), 1)

	for _, term := range []struct {
		name string
		q    string
	}{
		{name: "by the serial off the label", q: serial},
		{name: "by the order number off the confirmation mail", q: number},
	} {
		t.Run(term.name, func(t *testing.T) {
			view, err := warranties.NewStore(pool).Warranties(ctx, term.q)
			if err != nil {
				t.Fatalf("search warranties: %v", err)
			}
			if !view.Searching() {
				t.Fatal("the page did not run a search for a term long enough to be one")
			}
			if len(view.Rows) != 1 {
				t.Fatalf("searching %q found %d registrations, want 1", term.q, len(view.Rows))
			}
			got := view.Rows[0]
			if got.Serial != serial || got.Order != number {
				t.Errorf("found serial %q on order %q, want %q on %q",
					got.Serial, got.Order, serial, number)
			}
			if !got.InForce {
				t.Error("a warranty registered today reads as expired")
			}
			var owner string
			if err := pool.QueryRow(ctx, `SELECT user_id::text FROM orders WHERE order_number = $1`, number).Scan(&owner); err != nil {
				t.Fatalf("read the order's customer: %v", err)
			}
			if got.CustomerID != owner {
				t.Errorf("warranty row names customer %q, want the order's customer %q", got.CustomerID, owner)
			}
		})
	}

	view, err := warranties.NewStore(pool).Warranties(ctx, "SN-NOSUCHTHING")
	if err != nil {
		t.Fatalf("search warranties: %v", err)
	}
	if len(view.Rows) != 0 {
		t.Errorf("an unregistered serial found %d rows, want 0", len(view.Rows))
	}
}

// TestStore_WarrantiesShortTerm: the warranty lookup refuses to list everything.
func TestStore_WarrantiesShortTerm(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	registeredWarranty(t, "SN-"+strings.ToUpper(uuid.NewString()[:8]), 1)

	for _, term := range []string{"", " ", "A"} {
		view, err := warranties.NewStore(pool).Warranties(ctx, term)
		if err != nil {
			t.Fatalf("search warranties %q: %v", term, err)
		}
		if view.Searching() {
			t.Errorf("%q ran a search", term)
		}
		if len(view.Rows) != 0 {
			t.Errorf("%q listed %d registrations without being asked", term, len(view.Rows))
		}
	}
}

// TestStore_WarrantiesNextPage follows the search's next link through the
// desk's own mux. The search binds its page token to an address it spells out
// itself, apart from the route Routes registers, so a route rename that missed
// it leaves page two at a 404 while every direct call to the handler passes.
func TestStore_WarrantiesNextPage(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	number, lineID, userID := admintest.WarrantableOrder(t, pool, keyset.PageLimit)
	prefix := "SN-" + strings.ToUpper(uuid.NewString()[:8])
	for unit := 1; unit <= keyset.PageLimit; unit++ {
		if err := warranty.NewStore(pool).Register(ctx, number, lineID.String(), userID.String(),
			fmt.Sprintf("%s-%02d", prefix, unit), unit); err != nil {
			t.Fatalf("register unit %d: %v", unit, err)
		}
	}
	store := warranties.NewStore(pool)
	first, err := store.Warranties(ctx, number)
	if err != nil {
		t.Fatalf("search warranties: %v", err)
	}
	if first.Next == "" {
		t.Fatalf("%d registrations on one order gave no next link", keyset.PageLimit)
	}

	log := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	warranties.NewHandler(store, log).Routes(mux, admin.NewControl(log, nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, first.Next, http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s answered %d, want this desk's second page", first.Next, rec.Code)
	}
	if n := strings.Count(rec.Body.String(), `<td class="goen-admin__cellkey">`+prefix); n != 1 {
		t.Errorf("the second page lists %d of the order's registrations, want the 1 left after the first page", n)
	}
}

// registeredWarranty registers unit 1 of a new warrantable order of quantity
// units under serial, as the customer does from their order page.
func registeredWarranty(t *testing.T, serial string, quantity int32) (registered, orderNumber string) {
	t.Helper()
	number, lineID, userID := admintest.WarrantableOrder(t, pool, quantity)
	if err := warranty.NewStore(pool).Register(
		t.Context(), number, lineID.String(), userID.String(), serial, 1); err != nil {
		t.Fatalf("register warranty: %v", err)
	}
	return serial, number
}
