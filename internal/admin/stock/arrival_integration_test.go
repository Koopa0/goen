//go:build integration

package stock_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/user"
)

func TestExpectedArrivalIsAuditedAndShownOnlyForTheSelectedSoldOutVariant(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, actor := admintest.StaffContext(t, owner)
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
	s := stock.NewStore(staff)
	var id, productID uuid.UUID
	var sku, slug string
	var onShelf, safety int32
	var today time.Time
	if err = owner.QueryRow(ctx, `SELECT pv.id, pv.product_id, pv.sku, p.slug, pv.stock_quantity, pv.safety_stock, shop_today()
  FROM product_variants pv JOIN products p ON p.id = pv.product_id
  WHERE p.status = 'active' AND pv.is_active AND pv.stock_quantity <= pv.safety_stock
  ORDER BY pv.sku LIMIT 1`).Scan(&id, &productID, &sku, &slug, &onShelf, &safety, &today); err != nil {
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
		{raw: shoptime.Day(today), shown: true},
		{raw: shoptime.Day(today.AddDate(0, 0, 1)), shown: true},
		{raw: shoptime.Day(today.AddDate(0, 0, -1))},
		{raw: ""},
	} {
		if err = s.SetArrival(ctx, sku, arrivalDay(t, tc.raw)); err != nil {
			t.Fatal(err)
		}
		view, loadErr := product.NewStore(owner, slog.New(slog.DiscardHandler)).Load(ctx, slug, selection)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if view.SKU != sku || !view.Exact {
			t.Fatal("fixture did not select the intended variant")
		}
		if (view.ArrivalText() != "") != tc.shown {
			t.Fatalf("arrival %q shown = %v, want %v", tc.raw, view.ArrivalText() != "", tc.shown)
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
		if stored != tc.raw || currentStock != onShelf {
			t.Fatalf("date/stock = %q/%d, want %q/%d", stored, currentStock, tc.raw, onShelf)
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
		if err = s.SetArrival(ctx, rows[i].SKU, arrivalDay(t, tomorrow)); err != nil {
			t.Fatal(err)
		}
		view, loadErr := product.NewStore(owner, slog.New(slog.DiscardHandler)).Load(ctx, slug, selection)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if view.SKU != sku || view.ArrivalText() != "" {
			t.Fatal("selected variant borrowed another variant's date")
		}
		break
	}

	if err = s.SetArrival(t.Context(), sku, arrivalDay(t, tomorrow)); !errors.Is(err, audit.ErrNoActor) {
		t.Fatalf("actorless write = %v, want ErrNoActor", err)
	}
	var cleared bool
	if err = owner.QueryRow(ctx, `SELECT preorder_release_on IS NULL FROM product_variants WHERE id=$1`, id).Scan(&cleared); err != nil {
		t.Fatal(err)
	}
	if !cleared {
		t.Fatal("actorless date escaped its rolled-back audit")
	}

	log := slog.New(slog.DiscardHandler)
	handler := stock.NewHandler(s, log)
	ac := access.New(log, nil)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		missing := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodPost, "/admin/stock/arrival", strings.NewReader("sku=missing-arrival-sku&arrival_on="+tomorrow))
		missing.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		missingResponse := httptest.NewRecorder()
		ac.RequireStaff(handler.SetArrival)(missingResponse, missing)
		if missingResponse.Code != http.StatusNotFound || !strings.Contains(missingResponse.Body.String(), i18n.T(missing.Context(), i18n.KeyAdminNotFoundBody)) {
			t.Errorf("%s unknown variant = %d, missing translated refusal page", locale, missingResponse.Code)
		}
		body := url.Values{"sku": {sku}, "arrival_on": {"2026-02-30"}, "return": {"/admin/stock?q=" + url.QueryEscape(sku)}}
		req := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodPost, "/admin/stock/arrival", strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		ac.RequireStaff(handler.SetArrival)(rec, req)
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
	ac.RequireStaff(handler.SetArrival)(denied, request(user.NewContext(t.Context(), user.User{ID: actor.String(), Role: user.RoleCustomer})))
	if denied.Code != http.StatusNotFound {
		t.Fatalf("customer staff route = %d, want 404", denied.Code)
	}
	accepted := httptest.NewRecorder()
	ac.RequireStaff(handler.SetArrival)(accepted, request(ctx))
	if accepted.Code != http.StatusSeeOther || !strings.HasPrefix(accepted.Header().Get("Location"), "/admin/stock?") {
		t.Fatalf("valid date = %d %q", accepted.Code, accepted.Header().Get("Location"))
	}
	received := safety - onShelf + 1
	if err = s.Receive(ctx, sku, received, actor.String(), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err = owner.QueryRow(ctx, `SELECT preorder_release_on IS NULL FROM product_variants WHERE id=$1`, id).Scan(&cleared); err != nil {
		t.Fatal(err)
	}
	if !cleared {
		t.Error("received variant retains its expected arrival date")
	}
	view, err := product.NewStore(owner, slog.New(slog.DiscardHandler)).Load(ctx, slug, selection)
	if err != nil {
		t.Fatal(err)
	}
	if !view.CanBuy() || view.ArrivalText() != "" {
		t.Fatal("in-stock variant still shows an expected arrival date")
	}
	if _, err = owner.Exec(ctx, `SELECT record_inventory_movement($1, $2, 'adjustment', $3, 'admin', NULL, $4)`, id, -received, uuid.NewString(), actor); err != nil {
		t.Fatal(err)
	}
	view, err = product.NewStore(owner, slog.New(slog.DiscardHandler)).Load(ctx, slug, selection)
	if err != nil {
		t.Fatal(err)
	}
	if view.CanBuy() || view.ArrivalText() != "" {
		t.Fatal("sold-out variant resurrected the completed delivery's arrival date")
	}
}

func arrivalDay(t *testing.T, raw string) pgtype.Date {
	t.Helper()
	day, ok := stock.ParseArrival(raw)
	if !ok {
		t.Fatalf("invalid fixture arrival %q", raw)
	}
	return day
}

func TestArrivalAuditLockDoesNotBlockStockNotifications(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, owner)
	var id uuid.UUID
	if err := owner.QueryRow(ctx, `SELECT id FROM product_variants ORDER BY sku LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	cfg := owner.Config().Copy()
	cfg.ConnConfig.RuntimeParams["role"] = "admin"
	staff, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(staff.Close)
	tx, err := staff.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pgtx.Rollback(ctx, tx)
	if _, err := db.New(tx).LockVariantForChange(ctx, id); err != nil {
		t.Fatal(err)
	}
	referenceCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := owner.Exec(referenceCtx, `INSERT INTO stock_notifications (variant_id, email, locale) VALUES ($1, 'arrival-reference@goen.invalid', 'en')`, id); err != nil {
		t.Fatalf("arrival audit lock blocked a stock notification: %v", err)
	}
}

func TestArrivalWriteFailuresRemainOperational(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, owner)
	var id uuid.UUID
	var sku, before string
	if err := owner.QueryRow(ctx, `SELECT id, sku, coalesce(preorder_release_on::text, '') FROM product_variants ORDER BY sku LIMIT 1`).Scan(&id, &sku, &before); err != nil {
		t.Fatal(err)
	}
	var auditBefore int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE entity_id=$1`, id).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"update", "audit"} {
		for _, locale := range i18n.Locales() {
			t.Run(failure+"/"+locale.Tag(), func(t *testing.T) {
				requestCtx := i18n.WithLocale(ctx, locale)
				config := owner.Config().Copy()
				config.ConnConfig.RuntimeParams["role"] = "admin"
				if failure == "update" {
					config.ConnConfig.Tracer = cancelArrivalWrite{}
				} else {
					requestCtx = user.NewContext(requestCtx, user.User{ID: uuid.NewString(), Role: user.RoleStaff})
				}
				staff, err := pgxpool.NewWithConfig(requestCtx, config)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(staff.Close)
				var role string
				if err = staff.QueryRow(requestCtx, `SELECT current_user`).Scan(&role); err != nil || role != "admin" {
					t.Fatalf("arrival writer role = %q: %v", role, err)
				}
				var diagnostic bytes.Buffer
				log := slog.New(slog.NewTextHandler(&diagnostic, nil))
				handler := stock.NewHandler(stock.NewStore(staff), log)
				form := url.Values{"sku": {sku}, "arrival_on": {"2030-01-02"}, "return": {"/admin/stock?q=" + url.QueryEscape(sku)}}
				request := httptest.NewRequestWithContext(requestCtx, http.MethodPost, "/admin/stock/arrival", strings.NewReader(form.Encode()))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				response := httptest.NewRecorder()
				access.New(log, nil).RequireStaff(handler.SetArrival)(response, request)
				if response.Code != http.StatusInternalServerError {
					t.Errorf("%s arrival failure = %d, want 500", failure, response.Code)
				}
				if !strings.Contains(response.Body.String(), i18n.T(requestCtx, i18n.KeyAdminErrorBody)) {
					t.Error("arrival failure lacks the translated service explanation")
				}
				if strings.Contains(response.Body.String(), `aria-invalid="true"`) {
					t.Error("arrival failure marks the valid date as refused")
				}
				if !strings.Contains(diagnostic.String(), "level=ERROR") || !strings.Contains(diagnostic.String(), "error=") {
					t.Error("arrival failure lost its diagnostic")
				}
				var after string
				var auditAfter int
				if err = owner.QueryRow(ctx, `SELECT coalesce(preorder_release_on::text, '') FROM product_variants WHERE id=$1`, id).Scan(&after); err != nil {
					t.Fatal(err)
				}
				if err = owner.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE entity_id=$1`, id).Scan(&auditAfter); err != nil {
					t.Fatal(err)
				}
				if after != before || auditAfter != auditBefore {
					t.Errorf("failed arrival date/audit = %q/%d, want %q/%d", after, auditAfter, before, auditBefore)
				}
			})
		}
	}
}

type cancelArrivalWrite struct{}

func (cancelArrivalWrite) TraceQueryStart(ctx context.Context, _ *pgx.Conn, query pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(query.SQL, "-- name: SetVariantArrival :exec") {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		return canceled
	}
	return ctx
}

func (cancelArrivalWrite) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
