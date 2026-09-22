//go:build integration

package admin_test

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestEveryAdminQueueReachesBeyondItsFirstPage(t *testing.T) {
	p := isolatedAdminSeedPool(t)
	ctx := t.Context()
	exec := func(sql string) {
		t.Helper()
		if _, err := p.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`
 INSERT INTO users (email, full_name) SELECT 'paging-' || n || '@example.invalid', 'Paging customer' FROM generate_series(1, 101) n;
 INSERT INTO users (email, role) VALUES ('paging-staff@example.invalid', 'admin');
 INSERT INTO products (brand_id, category_id, slug, name)
 SELECT (SELECT id FROM brands LIMIT 1), (SELECT id FROM categories LIMIT 1), 'paging-' || n, 'Paging product ' || n FROM generate_series(1, 101) n;
 INSERT INTO product_variants (product_id, sku, price_cents, position, safety_stock)
 SELECT id, upper(slug), 100, 0, 10 FROM products WHERE slug LIKE 'paging-%';
 INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
 SELECT next_order_number(), v.id, sm.code, v.name FROM generate_series(1,101) n
 CROSS JOIN (SELECT id, method_id, name FROM shipping_method_versions ORDER BY effective_at LIMIT 1) v
 JOIN shipping_methods sm ON sm.id = v.method_id;
 INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
 SELECT id, 'paging-orders@example.invalid', 'Paging recipient', '0912345678', '110', 'Taipei', 'District', 'Street' FROM orders;
 INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
 SELECT id, 'PAGING-1', 'Paging product', 100, 51 FROM orders;
 SELECT open_payment(id, 'cs_paging_' || order_number, 5100) FROM orders;
 SELECT capture_payment('cs_paging_' || order_number, 5100, NULL, NULL) FROM orders;
 UPDATE orders SET fulfillment_status='picking';
 UPDATE orders SET fulfillment_status='shipped';
 INSERT INTO order_shipments (order_id, carrier, tracking_number)
 SELECT id, 'Paging carrier', order_number FROM orders;
 INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
 SELECT o.id, s.id, l.id, l.quantity FROM orders o JOIN order_shipments s ON s.order_id=o.id JOIN order_lines l ON l.order_id=o.id;
 INSERT INTO return_requests (order_id, reason) SELECT id, order_number FROM orders;
 INSERT INTO return_request_lines (return_request_id, order_line_id, quantity)
 SELECT r.id, l.id, 1 FROM return_requests r JOIN order_lines l ON l.order_id = r.order_id;
 INSERT INTO warranty_registrations (order_line_id, unit_no, serial_number, expires_on)
 SELECT (SELECT id FROM order_lines LIMIT 1), n, 'PAGING-' || n, current_date + 365 FROM generate_series(1,51) n;
 INSERT INTO coupons (code, description, kind, is_active)
 SELECT 'PAGING-' || n, 'Paging coupon', 'free_shipping', n <= 51 FROM generate_series(1,101) n;
 INSERT INTO sale_campaigns (slug, title, ends_at, is_active)
 SELECT 'paging-' || n, 'Paging campaign', now()+interval '1 day', n <= 51 FROM generate_series(1,101) n;
 INSERT INTO product_reviews (product_id, rating, body)
 SELECT (SELECT id FROM products LIMIT 1), 5, 'Paging review ' || n FROM generate_series(1,101) n;
 INSERT INTO contact_messages (name, email, subject, message, created_at, handled_at)
 SELECT 'Paging message', 'paging@example.invalid', '商品諮詢', 'Paging message ' || n,
 now() - interval '1 day' * (102 - n), CASE WHEN n > 51 THEN now() END FROM generate_series(1,101) n;
 SELECT grant_store_credit((SELECT id FROM users WHERE email='paging-1@example.invalid'), 100, 'Paging credit ' || n,
 (SELECT id FROM users WHERE email='paging-staff@example.invalid'), uuidv7()) FROM generate_series(1,101) n;
 SELECT record_inventory_movement((SELECT id FROM product_variants WHERE sku='PAGING-1'), 1, 'receipt', 'paging-' || n,
 'admin', NULL, (SELECT id FROM users WHERE email='paging-staff@example.invalid')) FROM generate_series(1,101) n;
 SELECT record_audit_event((SELECT id FROM users WHERE email='paging-staff@example.invalid'), 'product.update', 'products',
 NULL, NULL, jsonb_build_object('n', n), 'paging-' || n) FROM generate_series(1,401) n;
 `)
	s := admin.NewStore(p, fakeRefunder{}, nil, nil)
	var warrantyOrder string
	if err := p.QueryRow(ctx, `SELECT o.order_number FROM orders o JOIN order_lines l ON l.order_id=o.id JOIN warranty_registrations w ON w.order_line_id=l.id LIMIT 1`).Scan(&warrantyOrder); err != nil {
		t.Fatal(err)
	}
	type result struct {
		bound pages.ListBound
		keys  []string
	}
	cases := []struct {
		name     string
		countSQL string
		read     func(string) (result, error)
	}{
		{"orders", "SELECT count(*) FROM orders", func(after string) (result, error) {
			v, e := s.Orders(ctx, "", "", after)
			r := result{bound: v.ListBound}
			for _, x := range v.Orders {
				r.keys = append(r.keys, x.Number)
			}
			return r, e
		}},
		{"order search", "SELECT count(*) FROM orders", func(after string) (result, error) {
			v, e := s.Orders(ctx, "", "paging-orders", after)
			r := result{bound: v.ListBound}
			for _, x := range v.Orders {
				r.keys = append(r.keys, x.Number)
			}
			return r, e
		}},
		{"customers", "SELECT count(*) FROM users WHERE email LIKE 'paging-%'", func(after string) (result, error) {
			v, e := s.Customers(ctx, "paging-", after)
			r := result{bound: v.ListBound}
			for _, x := range v.Rows {
				r.keys = append(r.keys, x.ID)
			}
			return r, e
		}},
		{"products", "SELECT count(*) FROM products", func(after string) (result, error) {
			v, e := s.Products(ctx, after)
			r := result{bound: v.ListBound}
			for _, x := range v.Rows {
				r.keys = append(r.keys, x.Slug)
			}
			return r, e
		}},
		{"stock", "SELECT count(*) FROM product_variants", func(after string) (result, error) {
			v, e := s.Variants(ctx, false, after)
			r := result{bound: v.ListBound}
			for _, x := range v.Variants {
				r.keys = append(r.keys, x.SKU)
			}
			return r, e
		}},
		{"movements", "SELECT count(*) FROM inventory_movements m JOIN product_variants v ON v.id=m.variant_id WHERE v.sku='PAGING-1'", func(after string) (result, error) {
			v, e := s.Movements(ctx, "PAGING-1", after)
			r := result{bound: v.ListBound}
			for _, x := range v.Rows {
				r.keys = append(r.keys, strconv.Itoa(x.Running))
			}
			return r, e
		}},
		{"returns", "SELECT count(*) FROM return_requests", func(after string) (result, error) {
			v, e := s.Returns(ctx, after)
			r := result{bound: v.Bound}
			for _, x := range v.Rows {
				r.keys = append(r.keys, x.ID)
			}
			return r, e
		}},
		{"coupons", "SELECT count(*) FROM coupons", func(after string) (result, error) {
			v, e := s.Coupons(ctx, after)
			r := result{bound: v.ListBound}
			for _, x := range v.Rows {
				r.keys = append(r.keys, x.Code)
			}
			return r, e
		}},
		{"campaigns", "SELECT count(*) FROM sale_campaigns", func(after string) (result, error) {
			v, e := s.Campaigns(ctx, after)
			r := result{bound: v.ListBound}
			for _, x := range v.Rows {
				r.keys = append(r.keys, x.Slug)
			}
			return r, e
		}},
		{"reviews", "SELECT count(*) FROM product_reviews", func(after string) (result, error) {
			v, e := s.Reviews(ctx, after)
			r := result{bound: v.ListBound}
			for _, x := range v.Rows {
				r.keys = append(r.keys, x.ID)
			}
			return r, e
		}},
		{"messages", "SELECT count(*) FROM contact_messages", func(after string) (result, error) {
			v, e := s.Messages(ctx, after)
			r := result{bound: v.ListBound}
			for _, x := range v.Rows {
				r.keys = append(r.keys, x.ID)
			}
			return r, e
		}},
		{"credit", "SELECT count(*) FROM store_credit_entries", func(after string) (result, error) {
			v, e := s.Credit(ctx, after)
			r := result{bound: v.ListBound}
			for _, x := range v.Rows {
				r.keys = append(r.keys, x.Reason)
			}
			return r, e
		}},
		{"audit", "SELECT count(*) FROM audit_events", func(after string) (result, error) {
			v, e := s.Audit(ctx, after)
			r := result{bound: v.ListBound}
			for _, x := range v.Rows {
				r.keys = append(r.keys, x.RequestID)
			}
			return r, e
		}},
		{"warranty", "SELECT count(*) FROM warranty_registrations", func(after string) (result, error) {
			v, e := s.Warranties(ctx, warrantyOrder, after)
			r := result{bound: v.ListBound}
			for _, x := range v.Rows {
				r.keys = append(r.keys, x.Serial)
			}
			return r, e
		}},
	}
	h := adminHandlerOver(p, s)
	handlers := map[string]http.HandlerFunc{"orders": h.Orders, "order search": h.Orders, "customers": h.Customers, "products": h.Products, "stock": h.Variants, "movements": h.Movements, "returns": h.Returns, "coupons": h.Coupons, "campaigns": h.Campaigns, "reviews": h.Reviews, "messages": h.Messages, "credit": h.Credit, "audit": h.Audit, "warranty": h.Warranties}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var want int
			if err := p.QueryRow(ctx, tt.countSQL).Scan(&want); err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			after := ""
			for page := range 20 {
				r, err := tt.read(after)
				if err != nil {
					t.Fatal(err)
				}
				if page == 0 && r.bound.Next == "" {
					t.Fatal("first page has no next link despite the older fixture")
				}
				// The links returned by the store must survive the handler and template.
				target := r.bound.First
				if target == "" {
					u, _ := url.Parse(r.bound.Next)
					q := u.Query()
					q.Del("after")
					u.RawQuery = q.Encode()
					target = u.String()
				}
				u, _ := url.Parse(target)
				q := u.Query()
				if after != "" {
					q.Set("after", after)
				}
				u.RawQuery = q.Encode()
				req := httptest.NewRequestWithContext(i18n.WithLocale(ctx, i18n.En), http.MethodGet, u.String(), nil)
				req.SetPathValue("sku", "PAGING-1")
				rec := httptest.NewRecorder()
				handlers[tt.name](rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("GET %s returned %d", u, rec.Code)
				}
				if r.bound.Next != "" && !strings.Contains(rec.Body.String(), `href="`+html.EscapeString(r.bound.Next)+`"`) {
					t.Fatal("rendered page lost the next cursor")
				}
				if page > 0 && !strings.Contains(rec.Body.String(), "First page") {
					t.Fatal("handler lost cursor or template lost restart link")
				}
				if page > 0 && r.bound.First == "" {
					t.Fatal("later page has no way to restart")
				}
				for _, key := range r.keys {
					if seen[key] {
						t.Fatalf("row repeated across pages: %s", key)
					}
					seen[key] = true
				}
				if r.bound.Next == "" {
					if len(seen) != want {
						t.Fatalf("%d rows reachable, want %d", len(seen), want)
					}
					return
				}
				u, err = url.Parse(r.bound.Next)
				if err != nil {
					t.Fatal(err)
				}
				after = u.Query().Get("after")
			}
			t.Fatal("pagination never terminates")
		})
	}
	// An insertion ahead of the cursor must not shift the next customer page.
	first, err := s.Customers(ctx, "paging-", "")
	if err != nil {
		t.Fatal(err)
	}
	next, _ := url.Parse(first.Next)
	before, err := s.Customers(ctx, "paging-", next.Query().Get("after"))
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO users (email) VALUES ('paging-new@example.invalid')`)
	after, err := s.Customers(ctx, "paging-", next.Query().Get("after"))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(before.Rows) != fmt.Sprint(after.Rows) {
		t.Fatal("insertion ahead of the cursor shifted the next page")
	}
	// Follow a real rendered link with plain GETs, including the oldest-unhandled inbox order.
	req := httptest.NewRequestWithContext(i18n.WithLocale(ctx, i18n.En), http.MethodGet, "/admin/messages", nil)
	w := httptest.NewRecorder()
	h.Messages(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `rel="next"`) {
		t.Fatalf("inbox has no plain next link: status %d", w.Code)
	}
	inbox, err := s.Messages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inbox.Rows[0].Message != "Paging message 1" {
		t.Fatalf("oldest unhandled message was buried: %s", inbox.Rows[0].Message)
	}
	w = httptest.NewRecorder()
	h.Messages(w, httptest.NewRequestWithContext(ctx, http.MethodGet, inbox.Next, nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Paging message 51") {
		t.Fatalf("row 51 is unreachable through GET: status %d", w.Code)
	}
}
