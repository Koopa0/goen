//go:build integration

package cart_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestHandler_UpdateItemUnavailable holds that UpdateItem redirects with an
// unavailable notice instead of a 500 when the variant can no longer be sold;
// the line keeps its quantity.
func TestHandler_UpdateItemUnavailable(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil)

	// Each case makes the variant impossible to sell a different way; the line
	// keeps its quantity and the visitor is told, not sent to a 500.
	for _, tc := range []struct{ name, breakIt, restore string }{
		{
			"no sellable stock",
			`UPDATE product_variants SET stock_quantity = 0, safety_stock = 0 WHERE id = $1`,
			"",
		},
		{
			"inactive variant",
			`UPDATE product_variants SET is_active = false WHERE id = $1`,
			`UPDATE product_variants SET is_active = true WHERE id = $1`,
		},
		{
			"inactive product",
			`UPDATE products SET status = 'archived'
			 WHERE id = (SELECT product_id FROM product_variants WHERE id = $1)`,
			`UPDATE products SET status = 'active'
			 WHERE id = (SELECT product_id FROM product_variants WHERE id = $1)`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A sibling stays sellable: a product with nothing left to sell
			// cannot stay active, and this test is about the variant.
			vid, _, _ := ordertest.ThreeVariants(t, pool, "stockfix-unavail")
			var wasStock, wasSafety int32
			if err := pool.QueryRow(ctx,
				`SELECT stock_quantity, safety_stock FROM product_variants WHERE id = $1`,
				vid).Scan(&wasStock, &wasSafety); err != nil {
				t.Fatalf("read stock: %v", err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), //nolint:usetesting // t.Context is already cancelled in Cleanup
					`UPDATE product_variants SET stock_quantity = $2, safety_stock = $3, is_active = true WHERE id = $1`,
					vid, wasStock, wasSafety)
				if tc.restore != "" {
					_, _ = pool.Exec(context.Background(), tc.restore, vid) //nolint:usetesting // t.Context is already cancelled in Cleanup
				}
			})

			id, token := newCartSession(t, s)
			if _, err := pool.Exec(ctx,
				`INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 1)`,
				id, vid); err != nil {
				t.Fatalf("seed line: %v", err)
			}
			if _, err := pool.Exec(ctx, tc.breakIt, vid); err != nil {
				t.Fatalf("fixture: %v", err)
			}

			form := url.Values{"variant": {vid.String()}, "quantity": {"2"}}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/cart/items/update",
				strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test
			rec := httptest.NewRecorder()
			h.UpdateItem(rec, req)

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("update unavailable status = %d, want 303; body=%s", rec.Code, rec.Body.String())
			}
			loc := rec.Header().Get("Location")
			if loc != "/cart?qty=unavailable" {
				t.Fatalf("update unavailable redirect = %q, want /cart?qty=unavailable", loc)
			}

			var stored int32
			if err := pool.QueryRow(ctx,
				`SELECT quantity FROM cart_items WHERE cart_id = $1 AND variant_id = $2`,
				id, vid).Scan(&stored); err != nil {
				t.Fatalf("the refused update deleted the customer's line: %v", err)
			}
			if stored != 1 {
				t.Errorf("refused update left quantity %d, want the original 1", stored)
			}

			follow := httptest.NewRequestWithContext(ctx, http.MethodGet, loc, http.NoBody)
			follow.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test
			page := httptest.NewRecorder()
			h.Page(page, follow)
			if page.Code != http.StatusOK {
				t.Fatalf("cart page status = %d, want 200", page.Code)
			}
			want := i18n.T(ctx, i18n.KeyAddRefused)
			if !strings.Contains(page.Body.String(), want) {
				t.Fatalf("cart page does not show %q after unavailable update", want)
			}
		})
	}
}

// TestHandler_UpdateItemAdjustedNotice holds that UpdateItem shows the
// quantity-adjusted notice when an update was clamped.
func TestHandler_UpdateItemAdjustedNotice(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil)

	vid := ordertest.FreshVariant(t, pool, "stockfix-update-notice")
	var wasStock, wasSafety int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity, safety_stock FROM product_variants WHERE id = $1`,
		vid).Scan(&wasStock, &wasSafety); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), //nolint:usetesting // t.Context is already cancelled in Cleanup
			`UPDATE product_variants SET stock_quantity = $2, safety_stock = $3 WHERE id = $1`,
			vid, wasStock, wasSafety)
	})
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET stock_quantity = 5, safety_stock = 2 WHERE id = $1`,
		vid); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	id, token := newCartSession(t, s)
	if err := s.Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	form := url.Values{"variant": {vid.String()}, "quantity": {"10"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/cart/items/update",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test
	rec := httptest.NewRecorder()
	h.UpdateItem(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("update adjusted status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/cart?qty=adjusted" {
		t.Fatalf("update adjusted redirect = %q, want /cart?qty=adjusted", loc)
	}

	follow := httptest.NewRequestWithContext(ctx, http.MethodGet, loc, http.NoBody)
	follow.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test
	page := httptest.NewRecorder()
	h.Page(page, follow)
	want := i18n.T(ctx, i18n.KeyCartQuantityAdjusted)
	if !strings.Contains(page.Body.String(), want) {
		t.Fatalf("cart page does not show adjustment notice %q", want)
	}
}

// TestHandler_QuantityBounds holds the cart's quantity at the HTTP boundary:
// whatever a POST carries — nothing, zero, a negative, a number past int32, or
// text — a line holds between one and the cart's ceiling and never more than
// can be supplied, which is the only quantity a checkout quote can then name.
func TestHandler_QuantityBounds(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil)

	for _, quantity := range []string{
		"", "0", "-1", "-2147483648", "2147483647", "2147483648",
		"99999999999999999999", "1e3", "0x10", " 3 ", "１",
	} {
		t.Run(strconv.Quote(quantity), func(t *testing.T) {
			cartID, token := newCartSession(t, s)
			variant := ordertest.FreshVariant(t, pool, "quantity-bounds")

			for _, route := range []struct {
				path  string
				serve func(http.ResponseWriter, *http.Request)
			}{
				{"/cart/items", h.AddItem},
				{"/cart/items/update", h.UpdateItem},
			} {
				form := url.Values{"variant": {variant.String()}, "quantity": {quantity}}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, route.path,
					strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test
				res := httptest.NewRecorder()
				route.serve(res, req)
				if res.Code != http.StatusSeeOther {
					t.Fatalf("POST %s quantity=%q answered %d, want 303", route.path, quantity, res.Code)
				}

				var held, stock int
				err := pool.QueryRow(ctx, `
					SELECT coalesce((SELECT quantity FROM cart_items
					                 WHERE cart_id = $1 AND variant_id = $2), 0),
					       (SELECT stock_quantity - safety_stock FROM product_variants WHERE id = $2)`,
					cartID, variant).Scan(&held, &stock)
				if err != nil {
					t.Fatalf("read the cart line: %v", err)
				}
				if held < 0 || held > cart.MaxLineQuantity || held > stock {
					t.Errorf("after POST %s quantity=%q the line holds %d (supply %d, ceiling %d)",
						route.path, quantity, held, stock, cart.MaxLineQuantity)
				}
			}
		})
	}
}
