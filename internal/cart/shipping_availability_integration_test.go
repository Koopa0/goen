//go:build integration

package cart_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	"github.com/koopa0/goen/internal/orderaccess"
)

func TestCheckoutWithoutDeliveryRetainsTheDraftAndWritesNothing(t *testing.T) {
	ownerPool := dbtest.Pool(t)
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, seedErr := ownerPool.Exec(t.Context(), string(seed)); seedErr != nil {
		t.Fatal(seedErr)
	}
	if _, couponErr := ownerPool.Exec(t.Context(), `INSERT INTO coupons (code, description, kind, amount_cents) VALUES ('DELIVERY-DRAFT', 'Delivery fixture', 'amount', 100)`); couponErr != nil {
		t.Fatal(couponErr)
	}
	trace := &withdrawDelivery{owner: ownerPool}
	cfg := ownerPool.Config().Copy()
	cfg.MaxConns = 2
	cfg.ConnConfig.Tracer = trace
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, roleErr := conn.Exec(ctx, "SET ROLE store")
		return roleErr
	}
	appPool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(appPool.Close)
	var role string
	if err := appPool.QueryRow(t.Context(), "SELECT current_user").Scan(&role); err != nil || role != "store" {
		t.Fatalf("checkout role = %q, error %v, want store", role, err)
	}
	var variant uuid.UUID
	if err := ownerPool.QueryRow(t.Context(), `SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE p.status = 'active' AND pv.is_active AND pv.stock_quantity > pv.safety_stock ORDER BY pv.id LIMIT 1`).Scan(&variant); err != nil {
		t.Fatal(err)
	}
	s := cart.NewStore(appPool)
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, scenario := range []string{"initial", "before-submit", "during-placement"} {
			t.Run(locale.Tag()+"/"+scenario, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				if _, err := ownerPool.Exec(ctx, "UPDATE shipping_methods SET is_active = true"); err != nil {
					t.Fatal(err)
				}
				id, token := newCartSession(t, s)
				if err := s.Add(ctx, id, variant, 1); err != nil {
					t.Fatal(err)
				}
				before := deliveryWriteState(t, ownerPool, id, variant)
				var diagnostics bytes.Buffer
				h := cart.NewHandler(s, orderaccess.NewStore(appPool, false), slog.New(slog.NewJSONHandler(&diagnostics, nil)), false, testLimiter(), nil, nil)
				mux := http.NewServeMux()
				mux.HandleFunc("GET /checkout", h.Checkout)
				mux.HandleFunc("POST /checkout", h.PlaceOrder)
				request := func(method string, form url.Values) *httptest.ResponseRecorder {
					req := httptest.NewRequestWithContext(ctx, method, "/checkout", strings.NewReader(form.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cookie under test
					res := httptest.NewRecorder()
					mux.ServeHTTP(res, req)
					return res
				}
				withdraw := func() {
					if _, err := ownerPool.Exec(ctx, "UPDATE shipping_methods SET is_active = false"); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "initial" {
					withdraw()
				}
				get := request(http.MethodGet, nil)
				if get.Code != http.StatusOK {
					t.Errorf("GET /checkout = %d, want 200", get.Code)
				}
				controls := deliveryForm(t, get.Body.String())
				attempt := controls["idempotency"]
				if attempt == "" {
					t.Fatal("checkout lost its retry identity")
				}
				if scenario == "initial" {
					assertNoDelivery(t, get, locale)
				} else if controls["shipping"] == "" || controls["checkout_quote"] == "" {
					t.Fatal("available checkout has no selected method or quote")
				}
				form := url.Values{
					"idempotency": {attempt}, "checkout_quote": {controls["checkout_quote"]}, "shipping": {controls["shipping"]},
					"email": {"delivery@example.com"}, "name": {"Draft Recipient"}, "phone": {"0912345678"},
					"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"}, "street": {"松高路 88 號"},
					"note": {"Keep this draft"}, "coupon": {"DELIVERY-DRAFT"}, "invoice_type": {"donation"}, "invoice_donation_code": {"919"},
				}
				switch scenario {
				case "before-submit":
					withdraw()
				case "during-placement":
					// The second version read is inside placement, after the form's quote was rebuilt.
					trace.mu.Lock()
					trace.armed, trace.reads, trace.err = true, 0, nil
					trace.mu.Unlock()
				}
				post := request(http.MethodPost, form)
				if post.Code != http.StatusUnprocessableEntity || post.Header().Get("Location") != "" {
					t.Errorf("POST /checkout = %d, Location %q, want 422 without redirect", post.Code, post.Header().Get("Location"))
				}
				if scenario == "during-placement" {
					trace.mu.Lock()
					reads, traceErr, armed := trace.reads, trace.err, trace.armed
					trace.armed = false
					trace.mu.Unlock()
					if reads != 2 || armed || traceErr != nil {
						t.Fatalf("delivery withdrawal: reads %d, armed %v, error %v, want two reads and successful withdrawal", reads, armed, traceErr)
					}
				}
				assertNoDelivery(t, post, locale)
				got := deliveryForm(t, post.Body.String())
				want := map[string]string{"idempotency": attempt, "checkout_quote": "", "email": "delivery@example.com", "name": "Draft Recipient", "phone": "0912345678", "postal_code": "110", "city": "台北市", "district": "信義區", "street": "松高路 88 號", "note": "Keep this draft", "coupon": "DELIVERY-DRAFT", "invoice_type": "donation", "invoice_donation_code": "919"}
				for key := range got {
					if _, keep := want[key]; !keep {
						delete(got, key)
					}
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("refused checkout draft (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(before, deliveryWriteState(t, ownerPool, id, variant)); diff != "" {
					t.Errorf("checkout writes without delivery (-want +got):\n%s", diff)
				}
				if diagnostics.Len() != 0 || len(post.Result().Cookies()) != 0 {
					t.Errorf("known delivery refusal logged %q or replaced cookies %v", diagnostics.String(), post.Result().Cookies())
				}
			})
		}
	}
}

type withdrawDelivery struct {
	owner *pgxpool.Pool
	mu    sync.Mutex
	armed bool
	reads int
	err   error
}

func (tr *withdrawDelivery) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !strings.HasPrefix(data.SQL, "-- name: ShippingVersion :one") {
		return ctx
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.armed {
		tr.reads++
		if tr.reads == 2 {
			tr.armed = false
			_, tr.err = tr.owner.Exec(ctx, "UPDATE shipping_methods SET is_active = false")
		}
	}
	return ctx
}

func (*withdrawDelivery) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func deliveryWriteState(t *testing.T, p *pgxpool.Pool, id, variant uuid.UUID) []int64 {
	t.Helper()
	got := make([]int64, 7)
	err := p.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM orders), (SELECT count(*) FROM checkout_attempts), (SELECT count(*) FROM inventory_reservations), (SELECT count(*) FROM coupon_redemptions), (SELECT count(*) FROM outbox_messages), (SELECT quantity::bigint FROM cart_items WHERE cart_id = $1 AND variant_id = $2), (SELECT stock_quantity::bigint FROM product_variants WHERE id = $2)`, id, variant).Scan(&got[0], &got[1], &got[2], &got[3], &got[4], &got[5], &got[6])
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func deliveryForm(t *testing.T, body string) map[string]string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	attr := func(n *html.Node, key string) string {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
		return ""
	}
	var form *html.Node
	for n := range doc.Descendants() {
		if n.Type == html.ElementNode && n.Data == "form" && attr(n, "id") == "checkout-form" {
			form = n
			break
		}
	}
	if form == nil {
		t.Fatal("checkout response lost its form")
	}
	got := map[string]string{}
	for n := range form.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		name := attr(n, "name")
		if name == "" {
			continue
		}
		if n.Data == "input" && attr(n, "type") == "radio" {
			checked := false
			for _, a := range n.Attr {
				checked = checked || a.Key == "checked"
			}
			if !checked {
				continue
			}
		}
		switch n.Data {
		case "input":
			got[name] = attr(n, "value")
		case "textarea":
			var value strings.Builder
			for child := range n.Descendants() {
				if child.Type == html.TextNode {
					value.WriteString(child.Data)
				}
			}
			got[name] = value.String()
		}
	}
	return got
}

func assertNoDelivery(t *testing.T, res *httptest.ResponseRecorder, locale i18n.Locale) {
	t.Helper()
	message := "No delivery method is available for this basket. Change the items or contact us."
	if locale == i18n.ZhHant {
		message = "購物車中的商品目前沒有可用的配送方式。請調整商品，或聯絡我們。"
	}
	body := res.Body.String()
	if !strings.Contains(body, message) || !strings.Contains(body, `id="shipping-unavailable"`) || strings.Contains(body, `name="shipping"`) {
		t.Errorf("checkout did not show the known no-delivery state: %s", body)
	}
}
