//go:build integration

package coupons_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/coupons"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/coupon"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
)

func TestCouponUsageReleasesCancelledOrdersAndRetainsTheirHistory(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, owner)
	desk := coupons.NewStore(admintest.AdminRolePool(t, owner))
	checkout := cart.NewStore(checkoutPool(t, owner))
	code := "USAGE-" + strings.ToUpper(uuid.NewString()[:8])
	errs, err := desk.CreateCoupon(ctx, &coupons.Form{
		Code: code, Description: "One current checkout", Kind: coupon.Amount,
		Value: 100, MaxRedemptions: 1, PerCustomer: 1,
	})
	if err != nil || len(errs) != 0 {
		t.Fatalf("CreateCoupon = %v, %v", errs, err)
	}
	var variant, shipping uuid.UUID
	if err := owner.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv JOIN products p ON p.id=pv.product_id
		WHERE pv.is_active AND p.status='active' AND pv.price_cents >= 50000
		  AND pv.stock_quantity-pv.safety_stock > 4
		ORDER BY pv.id LIMIT 1`).Scan(&variant); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `
		SELECT v.id FROM shipping_method_versions v
		JOIN shipping_methods m ON m.id=v.method_id
		WHERE m.is_active AND m.destination_kind='address' AND v.effective_at<=now()
		ORDER BY v.effective_at DESC,v.id DESC LIMIT 1`).Scan(&shipping); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	coupons.NewHandler(desk, slog.New(slog.DiscardHandler)).Routes(mux, admintest.BackOffice)
	ledger := map[uuid.UUID]redemption{}
	assertCouponUsage(t, ctx, desk, mux, code, 0, 0)
	for n := 1; n <= 3; n++ {
		t.Run(fmt.Sprintf("checkout %d", n), func(t *testing.T) {
			number := placeCouponOrder(t, checkout, variant, shipping, code)
			var id uuid.UUID
			var state order.FulfillmentStatus
			if err := owner.QueryRow(ctx, `SELECT id,fulfillment_status FROM orders WHERE order_number=$1`, number).Scan(&id, &state); err != nil {
				t.Fatal(err)
			}
			if state != order.FulfillmentPending {
				t.Fatalf("placed fulfillment = %q, want pending", state)
			}
			var entry uuid.UUID
			if err := owner.QueryRow(ctx, `SELECT id FROM coupon_redemptions WHERE order_id=$1`, id).Scan(&entry); err != nil {
				t.Fatal(err)
			}
			ledger[id] = redemption{ID: entry, Cents: 10000}
			assertCouponUsage(t, ctx, desk, mux, code, 1, 10000)
			assertRedemptionHistory(t, owner, code, ledger)
			if n < 3 {
				if err := checkout.CancelOrder(t.Context(), number); err != nil {
					t.Fatalf("CancelOrder: %v", err)
				}
				if err := owner.QueryRow(ctx, `SELECT fulfillment_status FROM orders WHERE id=$1`, id).Scan(&state); err != nil {
					t.Fatal(err)
				}
				if state != order.FulfillmentCancelled {
					t.Fatalf("cancelled fulfillment = %q, want cancelled", state)
				}
				assertCouponUsage(t, ctx, desk, mux, code, 0, 0)
				assertRedemptionHistory(t, owner, code, ledger)
			}
		})
	}
}

func checkoutPool(t *testing.T, owner *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	cfg := owner.Config().Copy()
	cfg.MaxConns = 2
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE store`)
		return err
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	var role string
	if err := p.QueryRow(t.Context(), `SELECT current_user`).Scan(&role); err != nil || role != "store" {
		t.Fatalf("current_user = %q, want store: %v", role, err)
	}
	return p
}

func placeCouponOrder(t *testing.T, s *cart.Store, variant, shipping uuid.UUID, code string) string {
	t.Helper()
	ctx := t.Context()
	id, err := s.Create(ctx, uuid.NewString(), uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	if addErr := s.Add(ctx, id, variant, 1); addErr != nil {
		t.Fatal(addErr)
	}
	view, err := s.View(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Lines) != 1 || view.SubtotalCents < 50000 {
		t.Fatalf("checkout fixture = %+v, want one line worth at least NT$500", view)
	}
	delivery := &order.Delivery{
		Email: "coupon-usage@example.com", RecipientName: "Coupon buyer", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "路 1 號",
	}
	quote, err := s.QuoteShipping(ctx, shipping, view.SubtotalCents, delivery.PostalCode)
	if err != nil {
		t.Fatal(err)
	}
	shippingCents, err := quote.Total()
	if err != nil {
		t.Fatal(err)
	}
	shown, err := (cart.CheckoutQuote{
		CartID: id, Lines: []cart.CheckoutQuoteLine{{VariantID: variant, Quantity: 1, UnitCents: view.Lines[0].UnitCents}},
		ShippingVersionID: shipping, ShippingCents: shippingCents, CouponCode: code, DiscountCents: 10000,
	}).ID()
	if err != nil {
		t.Fatal(err)
	}
	attempt := uuid.New()
	number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipping, delivery, nil, code, shown,
		base64.RawURLEncoding.EncodeToString(attempt[:]))
	if err != nil {
		t.Fatalf("PlaceOrder with reusable one-use coupon: %v", err)
	}
	return number
}

func assertCouponUsage(t *testing.T, baseCtx context.Context, desk *coupons.Store, mux *http.ServeMux, code string, used, cents int64) {
	t.Helper()
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(baseCtx, locale)
		view, err := desk.Coupons(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for i := range view.Rows {
			row := &view.Rows[i]
			if row.Code != code {
				continue
			}
			found = true
			if diff := cmp.Diff([]int64{1, used, cents}, []int64{int64(row.MaxRedeem), row.Redeemed, row.GivenCents}); diff != "" {
				t.Errorf("%s current quota and discount (-want +got):\n%s", locale.Tag(), diff)
			}
			wantState := "Used up"
			if locale == i18n.ZhHant {
				wantState = "已用完"
			}
			if used == 0 {
				wantState = "Live"
				if locale == i18n.ZhHant {
					wantState = "使用中"
				}
			}
			if row.State(ctx) != wantState || row.Live() != (used == 0) {
				t.Errorf("%s coupon after %d active uses: state = %q, live = %t; want %q, %t", locale.Tag(), used, row.State(ctx), row.Live(), wantState, used == 0)
			}
		}
		if !found {
			t.Fatalf("coupon %s absent from list", code)
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/coupons", http.NoBody))
		if response.Code != http.StatusOK {
			t.Fatalf("GET /admin/coupons = %d, want 200", response.Code)
		}
		text := couponRowText(t, response.Body.String(), code)
		want := fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponUsed), used)
		if cents > 0 {
			want += fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCouponGiven), "NT$100")
		}
		want = fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCoupUsed), want)
		if !strings.Contains(text, want) {
			t.Errorf("%s coupon row = %q, want usage %q", locale.Tag(), text, want)
		}
	}
}

func TestCouponExpiryKeepsTheShopMinute(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, owner)
	desk := coupons.NewStore(admintest.AdminRolePool(t, owner))
	mux := http.NewServeMux()
	coupons.NewHandler(desk, slog.New(slog.DiscardHandler)).Routes(mux, admintest.BackOffice)
	for _, tt := range []struct {
		name string
		ends time.Time
		want string
	}{
		{name: "UTC timestamp", ends: time.Date(2027, time.January, 2, 3, 4, 0, 0, time.UTC), want: "2027-01-02 11:04"},
		{name: "no expiry"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code := "EXPIRY-" + strings.ToUpper(uuid.NewString()[:8])
			errs, err := desk.CreateCoupon(ctx, &coupons.Form{
				Code: code, Description: "Coupon expiry", Kind: coupon.Amount,
				Value: 100, PerCustomer: 1,
			})
			if err != nil || len(errs) != 0 {
				t.Fatalf("CreateCoupon = %v, %v", errs, err)
			}
			if !tt.ends.IsZero() {
				if _, updateErr := owner.Exec(ctx, `UPDATE coupons SET starts_at=$2, ends_at=$3 WHERE code=$1`, code, tt.ends.Add(-time.Hour), tt.ends); updateErr != nil {
					t.Fatal(updateErr)
				}
			}
			for _, locale := range i18n.Locales() {
				localized := i18n.WithLocale(ctx, locale)
				view, readErr := desk.Coupons(localized)
				if readErr != nil {
					t.Fatal(readErr)
				}
				found := false
				for _, row := range view.Rows {
					if row.Code == code {
						found = true
						if row.EndsAt != tt.want {
							t.Errorf("%s coupon expiry = %q, want %q", locale.Tag(), row.EndsAt, tt.want)
						}
					}
				}
				if !found {
					t.Fatalf("coupon %s absent from list", code)
				}
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, httptest.NewRequestWithContext(localized, http.MethodGet, "/admin/coupons", http.NoBody))
				if response.Code != http.StatusOK {
					t.Fatalf("GET /admin/coupons = %d, want 200", response.Code)
				}
				text := couponRowText(t, response.Body.String(), code)
				if tt.want != "" && !strings.Contains(text, tt.want) {
					t.Errorf("%s coupon row = %q, want expiry %q", locale.Tag(), text, tt.want)
				}
				if tt.want == "" && (strings.Contains(text, "Until ") || strings.Contains(text, "至 ")) {
					t.Errorf("%s coupon without expiry renders an end: %q", locale.Tag(), text)
				}
			}
		})
	}
}

func couponRowText(t *testing.T, body, code string) string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for node := range root.Descendants() {
		if node.Type != html.ElementNode || node.Data != "form" {
			continue
		}
		for _, attr := range node.Attr {
			if attr.Key != "action" || attr.Val != "/admin/coupons/"+code+"/active" {
				continue
			}
			for row := node.Parent; row != nil; row = row.Parent {
				if row.Type != html.ElementNode || row.Data != "tr" {
					continue
				}
				var text strings.Builder
				for part := range row.Descendants() {
					if part.Type == html.TextNode {
						text.WriteString(part.Data)
					}
				}
				return strings.Join(strings.Fields(text.String()), " ")
			}
		}
	}
	t.Fatalf("coupon row %s missing", code)
	return ""
}

type redemption struct {
	ID    uuid.UUID
	Cents int64
}

func assertRedemptionHistory(t *testing.T, p *pgxpool.Pool, code string, want map[uuid.UUID]redemption) {
	t.Helper()
	rows, err := p.Query(t.Context(), `
		SELECT r.order_id,r.id,r.amount_cents FROM coupon_redemptions r
		JOIN coupons c ON c.id=r.coupon_id WHERE c.code=$1`, code)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[uuid.UUID]redemption{}
	for rows.Next() {
		var id uuid.UUID
		var entry redemption
		if err := rows.Scan(&id, &entry.ID, &entry.Cents); err != nil {
			t.Fatal(err)
		}
		got[id] = entry
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("append-only redemption history (-want +got):\n%s", diff)
	}
}
