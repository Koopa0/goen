//go:build integration

package account_test

import (
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
)

func TestAccountOrdersReachEveryOlderOrderWithoutJavaScript(t *testing.T) {
	s := account.NewStore(pool)
	owner := register(t, s, "paged-"+uuid.NewString()+"@example.invalid")
	other := register(t, s, "other-paged-"+uuid.NewString()+"@example.invalid")
	ctx := account.WithUser(i18n.WithLocale(t.Context(), i18n.En), owner)
	if _, err := pool.Exec(ctx, `
 WITH inserted AS (
 INSERT INTO orders (user_id, shipping_version_id, shipping_method_code, shipping_method_name, placed_at)
 SELECT $1::uuid,v.id,sm.code,v.name,'2026-01-01T00:00:00Z'::timestamptz FROM generate_series(1,41) n
 CROSS JOIN (SELECT id,method_id,name FROM shipping_method_versions ORDER BY effective_at LIMIT 1) v
 JOIN shipping_methods sm ON sm.id=v.method_id RETURNING id
 ), lines AS (
 INSERT INTO order_lines (order_id,sku,product_name,unit_price_cents,quantity)
 SELECT id,'PAGING-ORDER','Paging order',100,1 FROM inserted
 )
 INSERT INTO order_private_data (order_id,email,recipient_name,phone,postal_code,city,district,street)
 SELECT id,'paging@example.invalid','Paging recipient','0912345678','110','Taipei','District','Street' FROM inserted`, owner.ID); err != nil {
		t.Fatal(err)
	}
	h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
	seen := map[string]bool{}
	target := "/account"
	for page := range 3 {
		u, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		v, err := s.Overview(ctx, owner, u.Query().Get("after"))
		if err != nil {
			t.Fatal(err)
		}
		want := 20
		if page == 2 {
			want = 1
		}
		if len(v.Orders) != want {
			t.Fatalf("page %d has %d orders, want %d", page, len(v.Orders), want)
		}
		for _, o := range v.Orders {
			if seen[o.Number] {
				t.Fatalf("repeated order %s", o.Number)
			}
			seen[o.Number] = true
		}
		w := httptest.NewRecorder()
		h.Overview(w, httptest.NewRequestWithContext(ctx, http.MethodGet, u.RequestURI(), nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET page %d: %d", page, w.Code)
		}
		if page > 0 && !strings.Contains(w.Body.String(), "Latest orders") {
			t.Fatal("later page lost its restart link")
		}
		if v.OrdersNext != "" && !strings.Contains(w.Body.String(), `href="`+html.EscapeString(v.OrdersNext)+`"`) {
			t.Fatal("next page is not a plain link")
		}
		if page == 0 {
			assertOrderCursorSurvivesInsertion(t, s, owner, other, v.OrdersNext)
		}
		target = v.OrdersNext
	}
	if len(seen) != 41 || target != "" {
		t.Fatalf("reached %d original orders; final next=%q", len(seen), target)
	}
}

func assertOrderCursorSurvivesInsertion(t *testing.T, s *account.Store, owner, other account.User, target string) {
	t.Helper()
	ctx := t.Context()

	next, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Overview(ctx, owner, next.Query().Get("after"))
	if err != nil {
		t.Fatal(err)
	}
	placeOrderFor(t, owner.ID)
	after, err := s.Overview(ctx, owner, next.Query().Get("after"))
	if err != nil {
		t.Fatal(err)
	}
	if before.Orders[0].Number != after.Orders[0].Number {
		t.Fatal("new order displaced the next page")
	}
	theirs, err := s.Overview(ctx, other, next.Query().Get("after"))
	if err != nil {
		t.Fatal(err)
	}
	if len(theirs.Orders) != 0 {
		t.Fatal("another account's cursor exposed orders")
	}
}
