//go:build integration

package admin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/shoptime"
)

func TestExpectedArrivalIsAuditedAndShownOnlyForTheSelectedSoldOutVariant(t *testing.T) {
	owner := isolatedAdminSeedPool(t)
	ctx, actor := staffContextOn(t, owner)
	cfg := owner.Config().Copy()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE admin")
		return err
	}
	staff, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(staff.Close)
	s := admin.NewStore(staff, fakeRefunder{}, nil, nil)
	var id, productID uuid.UUID
	var sku, slug string
	var stock, safety int32
	var today time.Time
	if err = owner.QueryRow(ctx, `SELECT pv.id, pv.product_id, pv.sku, p.slug, pv.stock_quantity, pv.safety_stock, shop_today()
  FROM product_variants pv JOIN products p ON p.id = pv.product_id
  WHERE p.status = 'active' AND pv.is_active AND pv.stock_quantity <= pv.safety_stock
  ORDER BY pv.sku LIMIT 1`).Scan(&id, &productID, &sku, &slug, &stock, &safety, &today); err != nil {
		t.Fatal(err)
	}
	rows, err := db.New(owner).ProductVariants(ctx, productID)
	if err != nil {
		t.Fatal(err)
	}
	selection := product.Selection{}
	for i := range rows {
		if rows[i].ID != id {
			continue
		}
		for j, name := range rows[i].OptionNames {
			selection[name] = rows[i].OptionValues[j]
		}
	}
	for _, tc := range []struct {
		raw   string
		shown bool
	}{
		{shoptime.Day(today), true}, {shoptime.Day(today.AddDate(0, 0, 1)), true},
		{shoptime.Day(today.AddDate(0, 0, -1)), false}, {"", false},
	} {
		if err = s.SetVariantArrival(ctx, sku, tc.raw); err != nil {
			t.Fatal(err)
		}
		view, loadErr := product.NewStore(owner).Load(ctx, slug, selection)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if view.SKU != sku || !view.Exact {
			t.Fatal("fixture did not select the intended variant")
		}
		if (view.ArrivalText(ctx) != "") != tc.shown {
			t.Fatalf("arrival %q shown = %v, want %v", tc.raw, view.ArrivalText(ctx) != "", tc.shown)
		}
		if view.CanBuy() {
			t.Fatal("arrival date made the sold-out variant buyable")
		}
		var currentStock int32
		var stored, audited string
		var recordedActor uuid.UUID
		if err = owner.QueryRow(ctx, `SELECT stock_quantity, coalesce(preorder_release_on::text, '') FROM product_variants WHERE id = $1`, id).Scan(&currentStock, &stored); err != nil {
			t.Fatal(err)
		}
		if stored != tc.raw || currentStock != stock {
			t.Fatalf("date/stock = %q/%d, want %q/%d", stored, currentStock, tc.raw, stock)
		}
		if err = owner.QueryRow(ctx, `SELECT after->>'preorder_release_on', actor_user_id FROM audit_events WHERE action = 'variant.arrival.set' AND entity_id = $1 ORDER BY occurred_at DESC, id DESC LIMIT 1`, id).Scan(&audited, &recordedActor); err != nil {
			t.Fatal(err)
		}
		if audited != tc.raw || recordedActor != actor {
			t.Fatal("arrival audit lost the date or staff identity")
		}
	}

	tomorrow := shoptime.Day(today.AddDate(0, 0, 1))
	for i := range rows {
		if rows[i].ID == id {
			continue
		}
		if err = s.SetVariantArrival(ctx, rows[i].SKU, tomorrow); err != nil {
			t.Fatal(err)
		}
		view, loadErr := product.NewStore(owner).Load(ctx, slug, selection)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if view.SKU != sku || view.ArrivalText(ctx) != "" {
			t.Fatal("selected variant borrowed another variant's date")
		}
		break
	}

	if err = s.SetVariantArrival(t.Context(), sku, tomorrow); !errors.Is(err, admin.ErrNoActor) {
		t.Fatalf("actorless write = %v, want ErrNoActor", err)
	}
	var cleared bool
	if err = owner.QueryRow(ctx, `SELECT preorder_release_on IS NULL FROM product_variants WHERE id=$1`, id).Scan(&cleared); err != nil {
		t.Fatal(err)
	}
	if !cleared {
		t.Fatal("actorless date escaped its rolled-back audit")
	}

	handler := adminHandlerOver(staff, s)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		body := url.Values{"sku": {sku}, "arrival_on": {"2026-02-30"}, "return": {"/admin/stock?q=" + url.QueryEscape(sku)}}
		req := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodPost, "/admin/stock/arrival", strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler.RequireStaff(handler.SetVariantArrival)(rec, req)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid date status = %d", rec.Code)
		}
		for _, want := range []string{`name="arrival_on" value="2026-02-30"`, `aria-invalid="true"`, `aria-describedby="arrival-error-` + sku + `"`} {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("refused date missing %q", want)
			}
		}
	}
	body := url.Values{"sku": {sku}, "arrival_on": {tomorrow}, "return": {"/admin/stock?q=" + url.QueryEscape(sku)}}
	request := func(ctx context.Context) *http.Request {
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/stock/arrival", strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req
	}
	denied := httptest.NewRecorder()
	handler.RequireStaff(handler.SetVariantArrival)(denied, request(account.WithUser(t.Context(), account.User{ID: actor.String(), Role: account.RoleCustomer})))
	if denied.Code != http.StatusNotFound {
		t.Fatalf("customer staff route = %d, want 404", denied.Code)
	}
	accepted := httptest.NewRecorder()
	handler.RequireStaff(handler.SetVariantArrival)(accepted, request(ctx))
	if accepted.Code != http.StatusSeeOther || !strings.HasPrefix(accepted.Header().Get("Location"), "/admin/stock?") {
		t.Fatalf("valid date = %d %q", accepted.Code, accepted.Header().Get("Location"))
	}
	if _, err = owner.Exec(ctx, `UPDATE product_variants SET is_active = true WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `SELECT record_inventory_movement($1, $2, 'adjustment', $3, 'admin', NULL, $4)`, id, safety-stock+1, uuid.NewString(), actor); err != nil {
		t.Fatal(err)
	}
	view, err := product.NewStore(owner).Load(ctx, slug, selection)
	if err != nil {
		t.Fatal(err)
	}
	if !view.CanBuy() || view.ArrivalText(ctx) != "" {
		t.Fatal("in-stock variant still shows an expected arrival date")
	}
}
