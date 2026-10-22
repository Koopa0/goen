//go:build integration

package cart_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
)

// TestHandler_PageShippingLookupFailure holds that the cart page tells
// unavailable delivery from a failed shipping lookup.
func TestHandler_PageShippingLookupFailure(t *testing.T) {
	owner := dbtest.Pool(t)
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(t.Context(), string(seed)); err != nil {
		t.Fatal(err)
	}
	trace := &cartPageShippingFailure{}
	cfg := owner.Config().Copy()
	cfg.MaxConns = 2
	cfg.ConnConfig.Tracer = trace
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, roleErr := conn.Exec(ctx, "SET ROLE store")
		return roleErr
	}
	app, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	var role string
	if err := app.QueryRow(t.Context(), "SELECT current_user").Scan(&role); err != nil || role != "store" {
		t.Fatalf("cart role = %q, error %v, want store", role, err)
	}
	var variant uuid.UUID
	if err := owner.QueryRow(t.Context(), `SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE p.status = 'active' AND pv.is_active AND pv.stock_quantity > pv.safety_stock ORDER BY pv.id LIMIT 1`).Scan(&variant); err != nil {
		t.Fatal(err)
	}
	s := cart.NewStore(app)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, tt := range []struct {
			name       string
			home       bool
			fail       bool
			noDelivery bool
		}{
			{name: "all methods inactive", noDelivery: true},
			{name: "home delivery available", home: true},
			{name: "shipping lookup fails", fail: true},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				if _, err := owner.Exec(ctx, "UPDATE shipping_methods SET is_active = ($1 AND code = 'home_delivery')", tt.home); err != nil {
					t.Fatal(err)
				}
				var active int
				if err := owner.QueryRow(ctx, "SELECT count(*) FROM shipping_methods WHERE is_active").Scan(&active); err != nil {
					t.Fatal(err)
				}
				wantActive := 0
				if tt.home {
					wantActive = 1
				}
				if active != wantActive {
					t.Fatalf("active shipping methods = %d, want %d", active, wantActive)
				}
				id, token := newCartSession(t, s)
				if err := s.Add(ctx, id, variant, 1); err != nil {
					t.Fatal(err)
				}
				before := deliveryWriteState(t, owner, id, variant)
				var diagnostics bytes.Buffer
				h := cart.NewHandler(s, access.NewStore(app, false), slog.New(slog.NewJSONHandler(&diagnostics, nil)), false, nil)
				mux := http.NewServeMux()
				mux.HandleFunc("GET /cart", h.Page)
				trace.mu.Lock()
				trace.fail, trace.starts, trace.ends, trace.err = tt.fail, 0, 0, nil
				trace.mu.Unlock()
				req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/cart", nil)
				req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cookie under test
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				trace.mu.Lock()
				starts, ends, queryErr := trace.starts, trace.ends, trace.err
				trace.fail = false
				trace.mu.Unlock()
				if starts != 1 || ends != 1 {
					t.Fatalf("ShippingChoices trace starts/ends = %d/%d, want 1/1", starts, ends)
				}
				if tt.fail {
					if !errors.Is(queryErr, context.Canceled) {
						t.Fatalf("ShippingChoices error = %v, want canceled child query", queryErr)
					}
				} else if queryErr != nil {
					t.Fatalf("ShippingChoices failed: %v", queryErr)
				}
				if ctx.Err() != nil {
					t.Fatalf("parent request context was canceled: %v", ctx.Err())
				}
				var alive int
				if err := app.QueryRow(ctx, "SELECT 1").Scan(&alive); err != nil || alive != 1 {
					t.Fatalf("parent context query = %d, error %v", alive, err)
				}
				if res.Code != http.StatusOK || res.Header().Get("Location") != "" {
					t.Fatalf("GET /cart = %d, Location %q, want 200 without redirect", res.Code, res.Header().Get("Location"))
				}
				message, checkout := "No delivery method is available for this cart. Change the items or contact us.", "Checkout"
				if locale == i18n.ZhHant {
					message, checkout = "購物車中的商品目前沒有可用的配送方式。請調整商品，或聯絡我們。", "前往結帳"
				}
				body := res.Body.String()
				if strings.Contains(body, message) != tt.noDelivery {
					t.Errorf("no-delivery message present = %v, want %v", strings.Contains(body, message), tt.noDelivery)
				}
				doc, err := html.Parse(strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				links, disabled := 0, 0
				for n := range doc.Descendants() {
					if n.Type != html.ElementNode {
						continue
					}
					attrs := map[string]string{}
					for _, a := range n.Attr {
						attrs[a.Key] = a.Val
					}
					if n.Data == "a" && attrs["href"] == "/checkout" {
						links++
					}
					if n.Data == "span" && attrs["aria-disabled"] == "true" {
						var text strings.Builder
						for child := range n.Descendants() {
							if child.Type == html.TextNode {
								text.WriteString(child.Data)
							}
						}
						if strings.TrimSpace(text.String()) == checkout {
							disabled++
						}
					}
				}
				wantLinks, wantDisabled := 1, 0
				if tt.noDelivery {
					wantLinks, wantDisabled = 0, 1
				}
				if links != wantLinks || disabled != wantDisabled {
					t.Errorf("checkout links/disabled controls = %d/%d, want %d/%d", links, disabled, wantLinks, wantDisabled)
				}
				if diff := cmp.Diff(before, deliveryWriteState(t, owner, id, variant)); diff != "" {
					t.Errorf("cart page writes (-want +got):\n%s", diff)
				}
				if len(res.Result().Cookies()) != 0 {
					t.Errorf("cart page replaced cookies: %v", res.Result().Cookies())
				}
			})
		}
	}
}

type cartPageShippingTraceKey struct{}

type cartPageShippingFailure struct {
	mu           sync.Mutex
	fail         bool
	starts, ends int
	err          error
}

func (tr *cartPageShippingFailure) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !strings.HasPrefix(data.SQL, "-- name: ShippingChoices :many") {
		return ctx
	}
	tr.mu.Lock()
	tr.starts++
	fail := tr.fail
	tr.mu.Unlock()
	if fail {
		child, cancel := context.WithCancel(ctx)
		cancel()
		ctx = child
	}
	return context.WithValue(ctx, cartPageShippingTraceKey{}, true)
}

func (tr *cartPageShippingFailure) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if ctx.Value(cartPageShippingTraceKey{}) != true {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.ends++
	tr.err = data.Err
}

func deliveryWriteState(t *testing.T, p *pgxpool.Pool, id, variant uuid.UUID) []int64 {
	t.Helper()
	got := make([]int64, 7)
	err := p.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM orders), (SELECT count(*) FROM checkout_attempts), (SELECT count(*) FROM inventory_reservations), (SELECT count(*) FROM coupon_redemptions), (SELECT count(*) FROM outbox_messages), (SELECT quantity::bigint FROM cart_items WHERE cart_id = $1 AND variant_id = $2), (SELECT stock_quantity::bigint FROM product_variants WHERE id = $2)`, id, variant).Scan(&got[0], &got[1], &got[2], &got[3], &got[4], &got[5], &got[6])
	if err != nil {
		t.Fatal(err)
	}
	return got
}
