//go:build integration

package access_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/buyer"
)

// TestStore_GrantSecondOrder: Store.Grant restarts the clock of a
// grant the browser carries from an earlier order when it grants a second, so a
// sweep does not drop the first order's grant while its cookie is still live.
// The cookie is re-issued with a fresh MaxAge on every order while a grant is
// swept on its own created_at, so equal durations are not equal deadlines
// unless the second order restarts it.
func TestStore_GrantSecondOrder(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), testLimiter(), false)

	first := checkouttest.PlaceUnpaidOrderFor(t, pool, s, "twice@example.com")
	firstCookie := placedCookie(t, first)
	second := checkouttest.PlaceUnpaidOrderFor(t, pool, s, "twice@example.com")

	retain := access.Retain.String()
	// Just inside the window: close enough to expiry that sweep would drop the row
	// without touch, but still live when Grant restarts the clock.
	if _, err := pool.Exec(ctx, `
		UPDATE order_access_grants
		SET created_at = now() - $1::interval + interval '1 second'
		WHERE order_id = (SELECT id FROM orders WHERE order_number = $2)`,
		retain, first); err != nil {
		t.Fatalf("age the first grant: %v", err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)
	r.AddCookie(firstCookie)
	if err := access.NewStore(pool, false).Grant(w, r, second); err != nil {
		t.Fatalf("remember the second order: %v", err)
	}
	var carried *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "goen_placed" {
			carried = c
		}
	}
	if carried == nil {
		t.Fatal("the second order set no cookie")
	}
	if !strings.Contains(carried.Value, firstCookie.Value) {
		t.Fatalf("the re-issued cookie dropped the first order's token; " +
			"this test would pass for the wrong reason")
	}

	// Sweep compares created_at with now(), so a grant that is only just inside
	// the window is not expired yet and the sweep would keep it even without the
	// touch. Moving both grants back 2 s puts an untouched first grant at Retain
	// + 1 s (swept) and a touched one at about 2 s (kept).
	if _, err := pool.Exec(ctx, `
		UPDATE order_access_grants SET created_at = created_at - interval '2 seconds'
		WHERE order_id IN (SELECT id FROM orders WHERE order_number = ANY($1))`,
		[]string{first, second}); err != nil {
		t.Fatalf("move both grants back 2 s: %v", err)
	}

	if err := access.NewStore(pool, false).Sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+first, http.NoBody)
	req.SetPathValue("number", first)
	req.AddCookie(carried)
	rec := httptest.NewRecorder()
	h.OrderPage(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("the browser that placed this order got %d for it, want 200 — its "+
			"grant was swept while the cookie carrying it was still live, which is "+
			"the state GrantRetain's own comment says must never happen", rec.Code)
	}
}

// TestStore_GrantStaleCarried: Store.Grant does not revive a
// carried grant at or past the retention when another order is placed, so the
// first order stays unreachable. TouchOrderAccessGrants must not revive a grant
// at or past GrantRetain; placing another order with the stale token carried
// forward must leave the first order unreachable.
func TestStore_GrantStaleCarried(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), testLimiter(), false)

	retain := access.Retain.String()

	for _, tc := range []struct {
		name   string
		offset string
	}{
		{name: "exact_boundary", offset: "0"},
		{name: "just_outside", offset: "-1 microsecond"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := checkouttest.PlaceUnpaidOrderFor(t, pool, s, tc.name+"@example.com")
			firstCookie := placedCookie(t, first)

			if _, err := pool.Exec(ctx, `
				UPDATE order_access_grants
				SET created_at = now() - $1::interval + $2::interval
				WHERE order_id = (SELECT id FROM orders WHERE order_number = $3)`,
				retain, tc.offset, first); err != nil {
				t.Fatalf("age the first grant: %v", err)
			}

			second := checkouttest.PlaceUnpaidOrderFor(t, pool, s, tc.name+"@example.com")
			w := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)
			r.AddCookie(firstCookie)
			if err := access.NewStore(pool, false).Grant(w, r, second); err != nil {
				t.Fatalf("remember the second order: %v", err)
			}
			var carried *http.Cookie
			for _, c := range w.Result().Cookies() {
				if c.Name == "goen_placed" {
					carried = c
				}
			}
			if carried == nil {
				t.Fatal("the second order set no cookie")
			}

			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+first, http.NoBody)
			req.SetPathValue("number", first)
			req.AddCookie(carried)
			rec := httptest.NewRecorder()
			h.OrderPage(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("a stale carried grant reached the first order: status %d, want 404", rec.Code)
			}
			if strings.Contains(rec.Body.String(), tc.name+"@example.com") {
				t.Error("a stale carried grant leaked the first order's email")
			}
		})
	}
}

// TestOrderAccessibleWithRetain: The OrderAccessibleWith query grants access
// only while a grant's age is strictly inside access.Retain: one microsecond
// inside, the exact boundary and one microsecond outside. Order access uses the
// database clock and a strict created_at > now() - retain. One transaction
// fixes now() across the fixture timestamps and the lookup so the equality case
// cannot flake.
func TestOrderAccessibleWithRetain(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	number := checkouttest.PlaceUnpaidOrderFor(t, pool, s, "retain-bound@example.com")
	cookie := placedCookie(t, number)
	token := cookie.Value
	if i := strings.IndexByte(token, '.'); i >= 0 {
		token = token[:i]
	}

	retain := access.Retain.String()
	retainParam := pgtype.Interval{
		Microseconds: int64(access.Retain / time.Microsecond), Valid: true,
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := db.New(tx)

	for _, tc := range []struct {
		name   string
		offset string
		want   bool
	}{
		{name: "just_inside", offset: "1 microsecond", want: true},
		{name: "exact_boundary", offset: "0", want: false},
		{name: "just_outside", offset: "-1 microsecond", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tx.Exec(ctx, `
				UPDATE order_access_grants
				SET created_at = now() - $1::interval + $2::interval
				WHERE order_id = (SELECT id FROM orders WHERE order_number = $3)`,
				retain, tc.offset, number); err != nil {
				t.Fatalf("set grant age: %v", err)
			}
			ok, err := q.OrderAccessibleWith(ctx, db.OrderAccessibleWithParams{
				OrderNumber: number,
				Digests:     [][]byte{bearer.Digest(token)},
				Retain:      retainParam,
			})
			if err != nil {
				t.Fatalf("OrderAccessibleWith: %v", err)
			}
			if ok != tc.want {
				t.Errorf("OrderAccessibleWith = %v, want %v", ok, tc.want)
			}
		})
	}
}

// TestStore_GrantUnknownOrder: Store.Grant for a number that names no order
// answers ErrNotFound and sets no cookie, so a caller cannot hand a browser a
// token that opens nothing.
func TestStore_GrantUnknownOrder(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

	err := access.NewStore(pool, false).Grant(w, r, "GO-000000-NOSUCH")
	if !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("Grant for an unknown order = %v, want ErrNotFound", err)
	}
	if cookies := w.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("Grant for an unknown order set %d cookies, want none", len(cookies))
	}
}
