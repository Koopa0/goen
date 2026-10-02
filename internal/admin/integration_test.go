//go:build integration

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/campaigns"
	contentdesk "github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/admin/customers"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/admin/loyalty"
	"github.com/koopa0/goen/internal/admin/reports"
	"github.com/koopa0/goen/internal/admin/shipping"
	roster "github.com/koopa0/goen/internal/admin/staff"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/site"
	"github.com/koopa0/goen/internal/ui/icons"
	"github.com/koopa0/goen/internal/ui/pages"
	adminpages "github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/warranty"
	"github.com/koopa0/goen/internal/web"
)

var pool *pgxpool.Pool

type returnQueryCountKey struct{}

type returnQueryTracer struct {
	queries *atomic.Int64
	mu      *sync.Mutex
	names   *[]string
}

func (t returnQueryTracer) TraceQueryStart(
	ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData,
) context.Context {
	if _, ok := ctx.Value(returnQueryCountKey{}).(struct{}); ok {
		t.queries.Add(1)
		name, _, _ := strings.Cut(data.SQL, "\n")
		t.mu.Lock()
		*t.names = append(*t.names, name)
		t.mu.Unlock()
	}
	return ctx
}

func (returnQueryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	if err := admintest.LoadCatalogue(context.Background(), pool); err != nil {
		slog.Error("load seed", "error", err)
		os.Exit(1)
	}

	code := m.Run()
	stop()
	os.Exit(code)
}

var backOffice = admintest.BackOffice

func healthHandler(p *pgxpool.Pool) *health.Handler {
	log := slog.New(slog.DiscardHandler)
	return health.NewHandler(health.NewStore(p), outbox.NewStore(p, log), nil, log)
}

func adminHandlerOver(p *pgxpool.Pool, s *admin.Store) *admin.Handler {
	log := slog.New(slog.DiscardHandler)
	return admin.NewHandler(admin.HandlerDeps{
		Store:   s,
		Images:  media.NewHandler(media.NewStore(p), log),
		Outbox:  outbox.NewStore(p, log),
		Letters: newsletter.NewStore(p),
		Log:     log,
	})
}

func TestProductReadsDistinguishAbsenceFromInfrastructure(t *testing.T) {
	ctx := t.Context()
	missing := "no-such-product-" + uuid.NewString()
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	_, err := s.Product(ctx, missing)
	if !errors.Is(err, admin.ErrNotFound) || errors.Is(err, admin.ErrRefused) {
		t.Fatalf("missing product = %v, want only ErrNotFound", err)
	}

	missingReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/products/"+missing, nil)
	missingReq.SetPathValue("slug", missing)
	missingRes := httptest.NewRecorder()
	adminHandlerOver(pool, s).EditProduct(missingRes, missingReq)
	if missingRes.Code != http.StatusNotFound {
		t.Fatalf("missing product page answered %d, want 404", missingRes.Code)
	}

	var existing string
	if readErr := pool.QueryRow(ctx, `SELECT slug FROM products ORDER BY slug LIMIT 1`).Scan(&existing); readErr != nil {
		t.Fatalf("read existing product: %v", readErr)
	}
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse timeout pool config: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "500"
	timeoutPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open timeout pool: %v", err)
	}
	t.Cleanup(timeoutPool.Close)
	timedStore := admin.NewStore(timeoutPool, fakeRefunder{}, nil, nil)

	blocker, err := pgx.Connect(ctx, pool.Config().ConnString())
	if err != nil {
		t.Fatalf("open blocker: %v", err)
	}
	t.Cleanup(func() { _ = blocker.Close(context.Background()) }) //nolint:usetesting // cleanup runs after t.Context is canceled
	tx, err := blocker.Begin(ctx)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, lockErr := tx.Exec(ctx, `LOCK TABLE products IN ACCESS EXCLUSIVE MODE`); lockErr != nil {
		t.Fatalf("lock products: %v", lockErr)
	}

	_, err = timedStore.Product(ctx, existing)
	if errors.Is(err, admin.ErrNotFound) || errors.Is(err, admin.ErrRefused) {
		t.Fatalf("product read timeout acquired a domain category: %v", err)
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != "57014" {
		t.Fatalf("product read failure = %v, want preserved PgError 57014", err)
	}

	timedHandler := adminHandlerOver(timeoutPool, timedStore)
	readReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/products/"+existing, nil)
	readReq.SetPathValue("slug", existing)
	readRes := httptest.NewRecorder()
	timedHandler.EditProduct(readRes, readReq)
	if readRes.Code != http.StatusInternalServerError {
		t.Fatalf("timed-out product page answered %d, want 500", readRes.Code)
	}

	badVariantReq := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/products/"+existing+"/variants", strings.NewReader("sku=&price="))
	badVariantReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badVariantReq.SetPathValue("slug", existing)
	badVariantRes := httptest.NewRecorder()
	timedHandler.AddVariant(badVariantRes, badVariantReq)
	if badVariantRes.Code != http.StatusInternalServerError {
		t.Fatalf("timed-out rejected-form rebuild answered %d, want 500", badVariantRes.Code)
	}
}

func TestProductUpdateDistinguishesAbsenceFromSuccess(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := draftProduct(t, ctx, s)
	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("read product fixture: %v", err)
	}
	missing := "no-such-product-" + uuid.NewString()
	form := &admin.ProductForm{
		Slug: missing, Name: view.Name, Summary: view.Summary,
		Description: view.Description, NameEn: view.NameEn,
		SummaryEn: view.SummaryEn, DescriptionEn: view.DescriptionEn,
		WarrantyNote: view.WarrantyNote, WarrantyMonths: view.WarrantyMonths,
		BrandID: view.BrandID, CategoryID: view.CategoryID,
	}
	before := auditRows(t, audit.ActionUpdateProduct)
	if errs, updateErr := s.UpdateProduct(ctx, form); !errors.Is(updateErr, admin.ErrNotFound) || len(errs) > 0 {
		t.Fatalf("UpdateProduct(absent) = %v, %v; want ErrNotFound and no field errors",
			errs, updateErr)
	}
	if after := auditRows(t, audit.ActionUpdateProduct); after != before {
		t.Fatalf("absent product update left %d audit rows, want %d", after, before)
	}

	values := url.Values{
		"name": {form.Name}, "summary": {form.Summary},
		"description": {form.Description}, "name_en": {form.NameEn},
		"summary_en": {form.SummaryEn}, "description_en": {form.DescriptionEn},
		"warranty":        {form.WarrantyNote},
		"warranty_months": {strconv.FormatInt(int64(form.WarrantyMonths), 10)},
		"brand":           {form.BrandID}, "category": {form.CategoryID},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/products/"+missing, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", missing)
	res := httptest.NewRecorder()
	adminHandlerOver(pool, s).UpdateProduct(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("UpdateProduct(absent) HTTP status = %d, want 404", res.Code)
	}
	if location := res.Header().Get("Location"); location != "" {
		t.Errorf("UpdateProduct(absent) redirected to %q; want no success redirect", location)
	}
}

func TestAdvanceRefusesAnUnfundedOrder(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	number := placeUnpaidOrder(t)
	_, err := s.Advance(ctx, number, "picking", uuid.NullUUID{})
	if err == nil {
		t.Fatal("an unpaid order was moved into fulfilment")
	}
	if !errors.Is(err, admin.ErrRefused) {
		t.Errorf("refused with %v, want ErrRefused", err)
	}
	if _, err := s.Advance(ctx, number, "cancelled", uuid.NullUUID{}); err != nil {
		t.Errorf("cancelling a pending order was refused: %v", err)
	}
}

func TestAdvanceRefusesAnIllegalTransition(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number := placeUnpaidOrder(t)

	if _, err := s.Advance(ctx, number, "shipped", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
		t.Errorf("pending -> shipped gave %v, want ErrRefused", err)
	}
	if _, err := s.Advance(ctx, number, "teleported", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
		t.Errorf("an unknown status gave %v, want ErrRefused", err)
	}
}

func placeUnpaidOrder(t *testing.T) string {
	t.Helper()
	return placeUnpaidOrderHolding(t, false)
}

// placeUnpaidOrderHolding is placeUnpaidOrder that, when holding, also carries a
// free unit of the variant with the most stock and holds it for 30 minutes, in
// the transaction placing the order as checkout does: no line or hold is taken
// against an order a later transaction did not place.
func placeUnpaidOrderHolding(t *testing.T, holding bool) string {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		SELECT next_order_number(), v.id, sm.code, v.name
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'ADMIN-TEST', '測試商品', 500000, 1)`, orderID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'x@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if holding {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity, position)
			SELECT $1, pv.id, pv.sku, p.name, 0, 1, 1
			FROM (SELECT id, sku, product_id FROM product_variants
			      ORDER BY stock_quantity DESC, id LIMIT 1) pv
			JOIN products p ON p.id = pv.product_id`, orderID); err != nil {
			t.Fatalf("put the held variant on the order: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			SELECT hold_inventory(ol.order_id, ol.variant_id,
				1, interval '30 minutes', 'deadline-actor:' || $2::text)
			FROM order_lines ol WHERE ol.order_id = $1 AND ol.variant_id IS NOT NULL`,
			orderID, number); err != nil {
			t.Fatalf("hold stock: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

func TestRetiringTheLastDiscountedVariantIsRefused(t *testing.T) {
	ctx := t.Context()

	var sku string
	if err := pool.QueryRow(ctx, `
		WITH one AS (
			SELECT pv.id, pv.sku, pv.product_id FROM product_variants pv
			WHERE pv.is_active AND pv.compare_at_price_cents IS NOT NULL
			ORDER BY pv.position LIMIT 1
		)
		SELECT sku FROM one`).Scan(&sku); err != nil {
		t.Skipf("the seed has no discounted variant to test with: %v", err)
	}

	var productID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT product_id FROM product_variants WHERE sku = $1`, sku).Scan(&productID); err != nil {
		t.Fatalf("read product: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE product_variants SET compare_at_price_cents = NULL
		WHERE product_id = $1 AND sku <> $2`, productID, sku); err != nil {
		t.Fatalf("clear siblings: %v", err)
	}

	var campaignID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO sale_campaigns (slug, title, ends_at)
		VALUES ($1, '測試活動', now() + interval '7 days') RETURNING id`,
		"admin-test-"+uuid.NewString()[:8]).Scan(&campaignID); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO sale_campaign_products (campaign_id, product_id) VALUES ($1, $2)`,
		campaignID, productID); err != nil {
		t.Fatalf("feature product: %v", err)
	}
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM sale_campaigns WHERE id = $1`, campaignID)
	})

	if err := stock.NewStore(pool).SetActive(ctx, sku, false); !errors.Is(err, admin.ErrRefused) {
		t.Errorf("retiring the last discounted variant of a featured product gave %v, "+
			"want ErrRefused — the campaign would point at nothing marked down", err)
	}
}

func pendingOrderHoldingStock(t *testing.T) (number string, orderID, variantID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.is_active AND p.status = 'active' AND pv.stock_quantity - pv.safety_stock > 2
		LIMIT 1`).Scan(&variantID); err != nil {
		t.Fatalf("find variant: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		SELECT next_order_number(), v.id, sm.code, v.name
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, 100000, 1 FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $2`, orderID, variantID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 's@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		orderID, variantID, "ship-test:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, orderID, variantID
}

func pickingOrderHoldingStock(t *testing.T) (number string, orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	number, orderID, _ = pendingOrderHoldingStock(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin funding: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, "cs_ship_"+number); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`, "cs_ship_"+number); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("to picking: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit funding: %v", err)
	}
	return number, orderID
}

func TestShipDoesAllFourWritesOrNone(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number, orderID := pickingOrderHoldingStock(t)

	if err := s.Ship(ctx, number, admin.Dispatch{Carrier: "black_cat", Tracking: "TW1234567890"}, uuid.NullUUID{}); err != nil {
		t.Fatalf("ship: %v", err)
	}

	var status, carrier, tracking string
	if err := pool.QueryRow(ctx, `
		SELECT o.fulfillment_status, sh.carrier, sh.tracking_number
		FROM orders o JOIN order_shipments sh ON sh.order_id = o.id
		WHERE o.id = $1`, orderID).Scan(&status, &carrier, &tracking); err != nil {
		t.Fatalf("read shipment: %v", err)
	}
	if status != "shipped" {
		t.Errorf("order is %q after shipping, want shipped", status)
	}
	if carrier != "black_cat" || tracking != "TW1234567890" {
		t.Errorf("shipment is %q/%q, want the carrier and tracking that were submitted", carrier, tracking)
	}

	var held int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM inventory_reservations WHERE order_id = $1 AND state = 'held'`,
		orderID).Scan(&held); err != nil {
		t.Fatalf("read reservations: %v", err)
	}
	if held != 0 {
		t.Errorf("%d reservations are still held after dispatch; a sweeper would "+
			"return stock that has already gone out", held)
	}

	var consumed int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM inventory_reservations WHERE order_id = $1 AND state = 'consumed'`,
		orderID).Scan(&consumed); err != nil {
		t.Fatalf("read reservations: %v", err)
	}
	if consumed != 1 {
		t.Errorf("%d reservations consumed, want 1", consumed)
	}

	var shippedQty int
	if scanErr := pool.QueryRow(ctx, `
		SELECT coalesce(sum(sl.quantity), 0) FROM order_shipment_lines sl
		WHERE sl.order_id = $1`, orderID).Scan(&shippedQty); scanErr != nil {
		t.Fatalf("read shipment lines: %v", scanErr)
	}
	if shippedQty != 1 {
		t.Errorf("%d units recorded as shipped, want 1 — a shipment with no "+
			"lines cannot be reconciled against a return", shippedQty)
	}

	var kinds []string
	rows, err := pool.Query(ctx,
		`SELECT kind FROM order_events WHERE order_id = $1 ORDER BY occurred_at, id`, orderID)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if scanErr := rows.Scan(&k); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		kinds = append(kinds, k)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate events: %v", err)
	}
	if len(kinds) == 0 || kinds[len(kinds)-1] != "shipped" {
		t.Errorf("history ends with %v, want a 'shipped' entry", kinds)
	}
}

func TestShippingIsRefusedForAnOrderThatWasNeverPicked(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, variantID := pendingOrderHoldingStock(t)

	var stockBefore int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, variantID).
		Scan(&stockBefore); err != nil {
		t.Fatalf("read stock before dispatch: %v", err)
	}

	err := s.Ship(ctx, number, admin.Dispatch{Carrier: "black_cat", Tracking: "TW999"}, actor)
	if !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("shipping a pending order gave %v, want ErrRefused", err)
	}

	var shipments, lines, held, consumed, events, notices int
	var stockAfter int32
	if queryErr := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM order_shipments WHERE order_id = $1),
		       (SELECT count(*) FROM order_shipment_lines WHERE order_id = $1),
		       (SELECT count(*) FROM inventory_reservations WHERE order_id = $1 AND state = 'held'),
		       (SELECT count(*) FROM inventory_reservations WHERE order_id = $1 AND state = 'consumed'),
		       (SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'shipped'),
		       (SELECT count(*) FROM outbox_messages
		        WHERE topic = 'order.shipped' AND payload->>'order_number' = $2),
		       (SELECT stock_quantity FROM product_variants WHERE id = $3)`,
		orderID, number, variantID).
		Scan(&shipments, &lines, &held, &consumed, &events, &notices, &stockAfter); queryErr != nil {
		t.Fatalf("read refused dispatch effects: %v", queryErr)
	}
	if shipments != 0 || lines != 0 {
		t.Errorf("refused dispatch left %d shipments and %d shipment lines, want none", shipments, lines)
	}
	if held != 1 || consumed != 0 {
		t.Errorf("refused dispatch left reservations held=%d consumed=%d, want 1/0", held, consumed)
	}
	if events != 0 || notices != 0 {
		t.Errorf("refused dispatch left %d shipped events and %d notices, want none", events, notices)
	}
	if stockAfter != stockBefore {
		t.Errorf("stock changed from %d to %d across a refused dispatch", stockBefore, stockAfter)
	}

	// Positive companion: the same order becomes shippable after it is funded
	// and enters picking. A fixture that can never ship would prove no boundary.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin funding: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ref := "cs_ship_" + number
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, ref); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`, ref); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move order to picking: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit funding: %v", err)
	}
	if err := s.Ship(ctx, number,
		admin.Dispatch{Carrier: "black_cat", Tracking: "TW999"}, actor); err != nil {
		t.Fatalf("ship the same order after picking: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read shipped status: %v", err)
	}
	if status != "shipped" {
		t.Errorf("same order ended %q after its admitted dispatch, want shipped", status)
	}
}

func TestShipNeedsACarrierAndATracking(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number, _ := pickingOrderHoldingStock(t)

	tests := []struct{ name, carrier, tracking string }{
		{"no carrier", "", "TW1"},
		{"no tracking", "black_cat", ""},
		{"a spelling outside the closed set", "黑貓", "TW1"},
		{"the display name is not the code", "黑貓宅急便", "TW1"},
		{"both blank", "", ""},
		{"whitespace only", "   ", "\t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.Ship(ctx, number, admin.Dispatch{Carrier: tt.carrier, Tracking: tt.tracking}, uuid.NullUUID{}); !errors.Is(err, admin.ErrInvalid) {
				t.Errorf("Ship(%q, %q) gave %v, want ErrInvalid", tt.carrier, tt.tracking, err)
			}
		})
	}
}

func TestAdvanceCannotShip(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number, orderID := pickingOrderHoldingStock(t)

	if _, err := s.Advance(ctx, number, "shipped", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("Advance to shipped gave %v, want ErrRefused", err)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "picking" {
		t.Errorf("order is %q, want picking — Advance moved it", status)
	}
}

// TestAdminCancelClawsBackLoyaltyPoints holds that a paid order the back office
// cancels — by refunding it before shipment — claws back its award lot,
// including when the lot was partly or wholly spent before cancellation.
func TestAdminCancelClawsBackLoyaltyPoints(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	t.Run("untouched lot", func(t *testing.T) {
		userID := cancelPointsCustomer(t)
		number, orderID := paidPickingOrderForUser(t, userID, 1200000)
		cancelPaidOrder(t, s, ctx, number)
		assertCancelClawback(t, orderID, -120, 120)
	})

	for _, tc := range []struct {
		name       string
		spent      int64
		wantPoints int64
	}{
		{"partly consumed", 100, -20},
		{"wholly consumed records a zero row", 120, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID := cancelPointsCustomer(t)
			number, orderID := paidPickingOrderForUser(t, userID, 1200000)
			if _, err := pool.Exec(ctx,
				`SELECT redeem_loyalty_points($1, $2, $3)`,
				userID, tc.spent, uuid.New()); err != nil {
				t.Fatalf("redeem: %v", err)
			}
			cancelPaidOrder(t, s, ctx, number)
			assertCancelClawback(t, orderID, tc.wantPoints, 120)
		})
	}

	t.Run("refuses before cancelled", func(t *testing.T) {
		userID := cancelPointsCustomer(t)
		_, pickingID := paidPickingOrderForUser(t, userID, 500000)
		var replay int64
		earlyErr := pool.QueryRow(ctx, `SELECT reverse_order_points($1)`, pickingID).Scan(&replay)
		if earlyErr == nil {
			t.Fatal("loyalty was reversed before the order was cancelled")
		}
		if constraintFrom(earlyErr) != "loyalty_clawback_cancelled_order" {
			t.Fatalf("refused by %q, want loyalty_clawback_cancelled_order: %v", constraintFrom(earlyErr), earlyErr)
		}
	})
}

func cancelPointsCustomer(t *testing.T) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('cancel-points-' || gen_random_uuid() || '@goen.invalid', 'customer', '取消點數')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return userID
}

func cancelPaidOrder(t *testing.T, s *admin.Store, ctx context.Context, number string) {
	t.Helper()
	if _, err := s.RefundBeforeShipment(ctx, number, "顧客取消"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

func assertCancelClawback(t *testing.T, orderID uuid.UUID, wantPoints, wantRequested int64) {
	t.Helper()
	ctx := t.Context()
	var points, requested int64
	var key string
	var returnID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT e.points, e.requested_points, e.idempotency_key, r.id
		FROM loyalty_entries e
		JOIN return_requests r ON r.order_id = e.order_id AND r.before_shipment
		WHERE e.order_id = $1 AND e.kind = 'clawback'`, orderID).Scan(&points, &requested, &key, &returnID); err != nil {
		t.Fatalf("read clawback: %v", err)
	}
	if points != wantPoints || requested != wantRequested {
		t.Errorf("clawback points/requested = %d/%d, want %d/%d",
			points, requested, wantPoints, wantRequested)
	}
	if want := "return:" + returnID.String(); key != want {
		t.Errorf("clawback key = %q, want %q", key, want)
	}
	var rows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM loyalty_entries
		WHERE order_id = $1 AND kind = 'clawback'`, orderID).Scan(&rows); err != nil {
		t.Fatalf("count clawbacks: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d cancel clawbacks, want 1", rows)
	}
	var replay int64
	if err := pool.QueryRow(ctx,
		`SELECT reverse_return_points($1)`, returnID).Scan(&replay); err != nil || replay != 0 {
		t.Fatalf("cancel clawback replay = %d, %v; want 0, nil", replay, err)
	}
}

func paidPickingOrderForUser(t *testing.T, userID uuid.UUID, cents int64) (number string, orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'CANCEL-POINTS', '點數取消', $2, 1)`, orderID, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'cancel-points@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`,
		orderID, "cs_cancel_pts_"+number, cents); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`,
		"cs_cancel_pts_"+number, cents); err != nil {
		t.Fatalf("capture: %v", err)
	}
	var awarded int64
	if err := tx.QueryRow(ctx, `SELECT award_loyalty_points($1)`, orderID).Scan(&awarded); err != nil {
		t.Fatalf("award: %v", err)
	}
	if awarded != cents/10000 {
		t.Fatalf("awarded = %d, want %d", awarded, cents/10000)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("to picking: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, orderID
}

func TestAdvanceRecordsWhoAndWhen(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number, orderID, _ := pendingOrderHoldingStock(t)

	var staff uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '出貨人員')
		RETURNING id`, "ship-"+number+"@goen.invalid").Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}

	if _, err := s.Advance(ctx, number, "cancelled", uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("advance: %v", err)
	}

	var kind string
	var actor uuid.NullUUID
	if err := pool.QueryRow(ctx, `
		SELECT kind, actor_user_id FROM order_events
		WHERE order_id = $1 ORDER BY occurred_at DESC, id DESC LIMIT 1`,
		orderID).Scan(&kind, &actor); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if kind != "cancelled" {
		t.Errorf("latest event is %q, want cancelled", kind)
	}
	if !actor.Valid || actor.UUID != staff {
		t.Errorf("event actor is %v, want the staff member who acted", actor)
	}
}

type fakeRefunder struct {
	failIntent bool
	refundErr  error
	state      admin.RefundState
	// sent counts calls to Refund. Local rows prove durable outcomes, but only
	// this provider-side counter can prove a retry did not execute twice.
	sent *atomic.Int64
}

func moveOrderToShipped(t *testing.T, tx pgx.Tx, orderID uuid.UUID) {
	t.Helper()
	for _, status := range []string{"picking", "shipped"} {
		if _, err := tx.Exec(t.Context(),
			`UPDATE orders SET fulfillment_status = $2 WHERE id = $1`,
			orderID, status); err != nil {
			t.Fatalf("move the order to %s: %v", status, err)
		}
	}
}

func (f fakeRefunder) PaymentIntentFor(_ context.Context, sessionID string) (string, error) {
	if f.failIntent {
		return "", errors.New("stripe is unreachable")
	}
	return "pi_for_" + sessionID, nil
}

func (f fakeRefunder) Refund(_ context.Context, intentID, requestKey string, _ int64) (string, admin.RefundState, error) {
	if f.sent != nil {
		f.sent.Add(1)
	}
	if f.refundErr != nil {
		return "", "", f.refundErr
	}
	state := f.state
	if state == "" {
		state = admin.RefundSucceeded
	}
	return "re_" + requestKey + "_" + intentID[:6], state, nil
}

func returnedOrder(t *testing.T, qty int32) (requestID uuid.UUID, orderNumber string) {
	t.Helper()
	return returnedOrderOn(t, pool, qty)
}

func returnedOrderOn(
	t *testing.T, p *pgxpool.Pool, qty int32,
) (requestID uuid.UUID, orderNumber string) {
	t.Helper()
	ctx := t.Context()

	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID, lineID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'REF-SKU', '測試商品', 100000, 2) RETURNING id`,
		orderID).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'f@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 200000)`,
		orderID, "cs_ret_"+orderNumber); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 200000, NULL, NULL)`,
		"cs_ret_"+orderNumber); err != nil {
		t.Fatalf("capture: %v", err)
	}
	moveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'black_cat', 'T-'||$2) RETURNING id`, orderID, orderNumber).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 2)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason) VALUES ($1, '不合用') RETURNING id`,
		orderID).Scan(&requestID); err != nil {
		t.Fatalf("create return request: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, $4)`, orderID, requestID, lineID, qty); err != nil {
		t.Fatalf("create return line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return requestID, orderNumber
}

// returnedOrderAtOn is the delivered sibling of returnedOrder: the rescission
// window is a read of the two explicit database clocks, so neither may inherit
// the test process's wall clock.
func returnedOrderAtOn(
	t *testing.T, p *pgxpool.Pool, delivered, requested time.Time,
) (requestID uuid.UUID) {
	t.Helper()
	return returnedOrderAtWithReasonOn(t, p, delivered, requested, "不合用")
}

func returnedOrderAtWithReasonOn(
	t *testing.T, p *pgxpool.Pool, delivered, requested time.Time, reason string,
) (requestID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID, lineID uuid.UUID
	var orderNumber string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'REF-CALENDAR', '鑑賞期日曆測試', 100000, 2) RETURNING id`,
		orderID).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'calendar-return@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	sessionID := "cs_ret_calendar_" + orderNumber
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 200000)`, orderID, sessionID); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 200000, NULL, NULL)`, sessionID); err != nil {
		t.Fatalf("capture: %v", err)
	}
	moveOrderToShipped(t, tx, orderID)

	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (
			order_id, carrier, tracking_number, shipped_at, delivered_at
		) VALUES ($1, 'black_cat', 'T-CALENDAR-' || $2, $3, $4)
		RETURNING id`, orderID, orderNumber, delivered.Add(-48*time.Hour), delivered).Scan(&shipmentID); err != nil {
		t.Fatalf("create delivered shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 2)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason, created_at)
		VALUES ($1, $2, $3) RETURNING id`, orderID, reason, requested).Scan(&requestID); err != nil {
		t.Fatalf("create return request: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 2)`, orderID, requestID, lineID); err != nil {
		t.Fatalf("create return line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return requestID
}

func deliveredOrderAt(t *testing.T, delivered time.Time) (number string, lineID uuid.UUID) {
	t.Helper()
	return deliveredOrderAtOn(t, pool, delivered)
}

func deliveredOrderAtOn(t *testing.T, p *pgxpool.Pool, delivered time.Time) (number string, lineID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'REF-POLICY', '政策視窗測試', 100000, 1) RETURNING id`,
		orderID).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'policy-return@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	sessionID := "cs_ret_policy_" + number
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, sessionID); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`, sessionID); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	moveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (
			order_id, carrier, tracking_number, shipped_at, delivered_at
		) VALUES ($1, 'black_cat', 'T-POLICY-' || $2, $3, $4)
		RETURNING id`, orderID, number, delivered.Add(-48*time.Hour), delivered).Scan(&shipmentID); err != nil {
		t.Fatalf("create delivered shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, lineID
}

// loyaltyReturn reaches the capture door that awards points, then constructs
// the already-delivered parcel a return decision consumes. prices are separate
// lines so returning one can distinguish proportional reversal from reversing
// the whole order.
func loyaltyReturn(t *testing.T, prices []int64, returnLine int) (
	requestID, orderID, userID uuid.UUID,
) {
	t.Helper()
	ctx := t.Context()
	var orderNumber string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('return-points-' || gen_random_uuid() || '@goen.invalid', 'customer', '點數退貨')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create customer: %v", err)
	}

	tx, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin order: %v", beginErr)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	lineIDs := make([]uuid.UUID, len(prices))
	var total int64
	for i, cents := range prices {
		total += cents
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, $2, '點數退貨商品', $3, 1, $4) RETURNING id`,
			orderID, fmt.Sprintf("RET-POINTS-%d-%s", i, orderID), cents, i).Scan(&lineIDs[i]); err != nil {
			t.Fatalf("create line %d: %v", i, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'points-return@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit order: %v", err)
	}

	payments := payment.NewStore(pool)
	session := "cs_return_points_" + orderID.String()
	if err := payments.OpenPayment(ctx, orderNumber, session, total); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	claimed, err := payments.ProcessWebhook(ctx, &payment.WebhookEvent{
		ID:   "evt_admin_return_points_" + uuid.NewString(),
		Type: "checkout.session.completed", ObjectRef: session,
		Payload: []byte(`{"object":"event"}`),
	}, func(ctx context.Context, tx *payment.WebhookTx) error {
		_, captureErr := tx.Capture(ctx, payment.Capture{
			SessionID: session, AmountRecv: total, Currency: payment.Currency,
		})
		return captureErr
	})
	if err != nil {
		t.Fatalf("capture and award: %v", err)
	}
	if !claimed {
		t.Fatal("the unique capture webhook was not claimed")
	}

	tx, beginErr = pool.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin delivery: %v", beginErr)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	moveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number, delivered_at)
		VALUES ($1, 'black_cat', 'RP-' || $2, now()) RETURNING id`,
		orderID, orderNumber).Scan(&shipmentID); err != nil {
		t.Fatalf("create delivered shipment: %v", err)
	}
	for i, lineID := range lineIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
			VALUES ($1, $2, $3, 1)`, orderID, shipmentID, lineID); err != nil {
			t.Fatalf("ship line %d: %v", i, err)
		}
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, requested_by_user_id, reason)
		VALUES ($1, $2, '不合用') RETURNING id`, orderID, userID).Scan(&requestID); err != nil {
		t.Fatalf("create return: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, requestID, lineIDs[returnLine]); err != nil {
		t.Fatalf("create return line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit return: %v", err)
	}
	return requestID, orderID, userID
}

func TestAReturnTakesBackItsPointsAndSpend(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	t.Run("full return", func(t *testing.T) {
		requestID, orderID, userID := loyaltyReturn(t, []int64{1200000}, 0)
		if err := s.Decide(ctx, requestID.String(), "approved", "全額退貨", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("settle return: %v", err)
		}
		assertReturnedLoyalty(t, requestID, orderID, userID, 0, -120)

		// The already-paid retry is refused, but must not post a second clawback.
		_ = s.Decide(ctx, requestID.String(), "approved", "重試", "", uuid.NullUUID{})
		var rows int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM loyalty_entries
			WHERE order_id = $1 AND kind = 'clawback'`, orderID).Scan(&rows); err != nil {
			t.Fatalf("count retried clawbacks: %v", err)
		}
		if rows != 1 {
			t.Errorf("retry left %d clawbacks, want 1", rows)
		}
		var tier uuid.NullUUID
		if err := pool.QueryRow(ctx,
			`SELECT member_tier($1, $2, NULL)`, userID, loyalty.MembershipWindowDays).Scan(&tier); err != nil {
			t.Fatalf("read tier: %v", err)
		}
		if tier.Valid {
			t.Errorf("fully returned only order still grants tier %s", tier.UUID)
		}
	})

	t.Run("partial return", func(t *testing.T) {
		requestID, orderID, userID := loyaltyReturn(t, []int64{700000, 500000}, 1)
		// Change today's tier AFTER the award. A clawback derived from member_tier
		// at return time takes 65 points; the durable 120-point award lot says this
		// 5/12 return owns 50. This is the temporal mismatch the fixture locks.
		addTierSpend(t, userID, 5000000)
		var multiplier int32
		if err := pool.QueryRow(ctx, `
			SELECT t.points_multiplier_bp
			FROM membership_tiers t
			WHERE t.id = member_tier($1, $2, $3)`,
			userID, loyalty.MembershipWindowDays, orderID).Scan(&multiplier); err != nil {
			t.Fatalf("read changed return-time tier: %v", err)
		}
		if multiplier != 13000 {
			t.Fatalf("return-time multiplier = %d, want 13000; the fixture cannot distinguish the old recomputation", multiplier)
		}
		var refunded int64
		if err := pool.QueryRow(ctx, `SELECT return_refundable_amount($1)`, requestID).Scan(&refunded); err != nil {
			t.Fatalf("read refundable amount: %v", err)
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "部分退貨", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("settle return: %v", err)
		}
		assertReturnedLoyalty(t, requestID, orderID, userID,
			5000000+1200000-refunded, -(refunded / 10000))
	})
}

func addTierSpend(t *testing.T, userID uuid.UUID, cents int64) {
	t.Helper()
	ctx := t.Context()
	var orderID uuid.UUID
	var number string
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tier order: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &number); err != nil {
		t.Fatalf("create tier order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1::uuid, 'RET-TIER-' || ($1::uuid)::text, '等級測試商品', $2, 1)`, orderID, cents); err != nil {
		t.Fatalf("create tier line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'tier-return@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create tier private data: %v", err)
	}
	ref := "cs_return_tier_" + orderID.String()
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3::bigint)`, orderID, ref, cents); err != nil {
		t.Fatalf("open tier payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2::bigint, NULL, NULL)`, ref, cents); err != nil {
		t.Fatalf("capture tier payment: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tier order %s: %v", number, err)
	}
}

// TestAClawbackOnlyFailureRemainsRetryable models the narrow commit boundary
// after the money and customer timeline have landed but before the separate
// points posting. A queue derived only from money calls this return complete,
// hides the retry control, and strands the missing clawback forever.
func TestAClawbackOnlyFailureRemainsRetryable(t *testing.T) {
	ctx, staff := staffContext(t)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, orderID, _ := loyaltyReturn(t, []int64{1200000}, 0)
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests
		SET status = 'approved', decided_at = now(), resolution = '退款完成'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve return: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, reason,
		                     return_request_id, status, provider_ref, succeeded_at)
		SELECT p.id, 'return:' || ($1::uuid)::text, 1200000, '退款完成', $1::uuid,
		       'succeeded', 're_points_gap_' || ($1::uuid)::text, now()
		FROM payments p
		WHERE p.order_id = $2 AND p.status = 'succeeded'`, requestID, orderID); err != nil {
		t.Fatalf("settle money before clawback: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_events (order_id, kind, return_request_id)
		VALUES ($1, 'refunded', $2)`, orderID, requestID); err != nil {
		t.Fatalf("record timeline before clawback: %v", err)
	}

	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	if err := s.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: returnLineID(t, requestID), Received: 1, Restocked: 0,
	}}, actor); err != nil {
		t.Fatalf("inspect points-gap return: %v", err)
	}
	completeErr := s.CompleteReturn(ctx, requestID.String(), "已驗貨", actor)
	pgErr, ok := errors.AsType[*pgconn.PgError](completeErr)
	if !errors.Is(completeErr, admin.ErrRefused) || !ok ||
		pgErr.ConstraintName != "return_requests_completed_points_settled" {
		t.Fatalf("completion without clawback = %v, want return_requests_completed_points_settled",
			completeErr)
	}
	view, err := s.Returns(ctx)
	if err != nil {
		t.Fatalf("read queue: %v", err)
	}
	for i := range view.Rows {
		if view.Rows[i].ID == requestID.String() {
			if !view.Rows[i].PayoutOutstanding || view.Rows[i].PayoutBlocked {
				t.Fatalf("points-only gap renders outstanding=%v blocked=%v, want true/false",
					view.Rows[i].PayoutOutstanding, view.Rows[i].PayoutBlocked)
			}
			goto retry
		}
	}
	t.Fatalf("return %s is absent from queue", requestID)

retry:
	if decideErr := s.Decide(ctx, requestID.String(), "approved", "補登點數", "", uuid.NullUUID{}); decideErr != nil {
		t.Fatalf("retry missing clawback: %v", decideErr)
	}
	var points, requested int64
	if queryErr := pool.QueryRow(ctx, `
		SELECT points, requested_points
		FROM loyalty_entries
		WHERE return_request_id = $1 AND kind = 'clawback'`, requestID).
		Scan(&points, &requested); queryErr != nil {
		t.Fatalf("read retried clawback: %v", queryErr)
	}
	if points != -120 || requested != 120 {
		t.Errorf("retried clawback = %d requested %d, want -120/120", points, requested)
	}
	if completeErr := s.CompleteReturn(ctx, requestID.String(), "已退款、驗貨並回收點數", actor); completeErr != nil {
		t.Fatalf("complete return after clawback retry: %v", completeErr)
	}
	view, err = s.Returns(ctx)
	if err != nil {
		t.Fatalf("read repaired queue: %v", err)
	}
	for i := range view.Rows {
		if view.Rows[i].ID == requestID.String() && view.Rows[i].PayoutOutstanding {
			t.Error("completed clawback still offers a payout retry")
		}
	}
}

// TestASettledReturnCanClawPointsBackAfterOwnerErasure pins the recovery order:
// card money may settle, the user may then lawfully erase their account, and
// the separately durable loyalty clawback must still be visible and runnable.
// The award lot and loyalty account are retained accounting records even though
// orders.user_id is detached.
func TestASettledReturnCanClawPointsBackAfterOwnerErasure(t *testing.T) {
	ctx, staff := staffContext(t)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, orderID, userID := loyaltyReturn(t, []int64{1200000}, 0)
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests
		SET status = 'approved', decided_at = now(), resolution = '退款完成'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve return: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, reason,
		                     return_request_id, status, provider_ref, succeeded_at)
		SELECT p.id, 'return:' || ($1::uuid)::text, 1200000, '退款完成', $1::uuid,
		       'succeeded', 're_erased_points_gap_' || ($1::uuid)::text, now()
		FROM payments p
		WHERE p.order_id = $2 AND p.status = 'succeeded'`, requestID, orderID); err != nil {
		t.Fatalf("settle money before erasure: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_events (order_id, kind, return_request_id)
		VALUES ($1, 'refunded', $2)`, orderID, requestID); err != nil {
		t.Fatalf("record timeline before erasure: %v", err)
	}

	if err := account.NewStore(pool).Erase(ctx, userID.String()); err != nil {
		t.Fatalf("erase owner after money settled: %v", err)
	}
	var detached bool
	if err := pool.QueryRow(ctx, `
		SELECT user_id IS NULL FROM orders WHERE id = $1`, orderID).Scan(&detached); err != nil {
		t.Fatalf("read erased order owner: %v", err)
	}
	if !detached {
		t.Fatal("erasure did not detach the order owner")
	}

	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	view, err := s.Returns(ctx)
	if err != nil {
		t.Fatalf("read erased-owner recovery queue: %v", err)
	}
	found := false
	for i := range view.Rows {
		if view.Rows[i].ID != requestID.String() {
			continue
		}
		found = true
		if !view.Rows[i].PayoutOutstanding || view.Rows[i].PayoutBlocked {
			t.Fatalf("erased-owner clawback renders outstanding=%v blocked=%v, want true/false",
				view.Rows[i].PayoutOutstanding, view.Rows[i].PayoutBlocked)
		}
	}
	if !found {
		t.Fatalf("return %s is absent from the erased-owner recovery queue", requestID)
	}

	if err := s.Decide(ctx, requestID.String(), "approved", "補登點數", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("retry clawback after erasure: %v", err)
	}
	var points, requested int64
	if err := pool.QueryRow(ctx, `
		SELECT points, requested_points
		FROM loyalty_entries
		WHERE return_request_id = $1 AND kind = 'clawback'`, requestID).
		Scan(&points, &requested); err != nil {
		t.Fatalf("read post-erasure clawback: %v", err)
	}
	if points != -120 || requested != 120 {
		t.Errorf("post-erasure clawback = %d requested %d, want -120/120", points, requested)
	}

	if err := s.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: returnLineID(t, requestID), Received: 1, Restocked: 0,
	}}, actor); err != nil {
		t.Fatalf("inspect erased-owner return: %v", err)
	}
	if err := s.CompleteReturn(ctx, requestID.String(), "已退款、驗貨並回收點數", actor); err != nil {
		t.Fatalf("complete erased-owner return after clawback: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read completed erased-owner return: %v", err)
	}
	if status != "completed" {
		t.Errorf("erased-owner return status = %q, want completed", status)
	}
}

// TestPointClawbackAndErasureShareOrderBeforeAccount forces the former ABBA
// window. The clawback pauses at its ledger insert; erasure has already begun.
// Both must finish in order, with neither PostgreSQL transaction chosen as a
// deadlock victim.
func TestPointClawbackAndErasureShareOrderBeforeAccount(t *testing.T) {
	ctx := t.Context()
	requestID, orderID, userID := loyaltyReturn(t, []int64{1200000}, 0)
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests
		SET status = 'approved', decided_at = now(), resolution = 'lock order'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve lock-order return: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, reason,
		                     return_request_id, status, provider_ref, succeeded_at)
		SELECT p.id, 'return:' || ($1::uuid)::text, 1200000, 'lock order', $1::uuid,
		       'succeeded', 're_lock_order_' || ($1::uuid)::text, now()
		FROM payments p
		WHERE p.order_id = $2 AND p.status = 'succeeded'`, requestID, orderID); err != nil {
		t.Fatalf("settle lock-order return: %v", err)
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_pause_return_clawback_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_pause_return_clawback_" + suffix}.Sanitize()
	const barrierKey int64 = 8_812_233_445_566_781
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF NEW.return_request_id = '%s'::uuid THEN
				PERFORM pg_advisory_xact_lock(%d);
			END IF;
			RETURN NEW;
		END
		$body$;
		CREATE TRIGGER %s BEFORE INSERT ON loyalty_entries
		FOR EACH ROW EXECUTE FUNCTION %s()`,
		functionName, requestID, barrierKey, triggerName, functionName)); err != nil {
		t.Fatalf("install clawback barrier: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON loyalty_entries; DROP FUNCTION IF EXISTS %s()",
			triggerName, functionName))
	})

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin clawback barrier: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, lockErr := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, barrierKey); lockErr != nil {
		t.Fatalf("hold clawback barrier: %v", lockErr)
	}
	reverseConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire clawback connection: %v", err)
	}
	defer reverseConn.Release()
	eraseConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire erasure connection: %v", err)
	}
	defer eraseConn.Release()
	var reversePID, erasePID int
	if err := reverseConn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&reversePID); err != nil {
		t.Fatalf("read clawback backend: %v", err)
	}
	if err := eraseConn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&erasePID); err != nil {
		t.Fatalf("read erasure backend: %v", err)
	}

	type reverseResult struct {
		points int64
		err    error
	}
	reverseResultCh := make(chan reverseResult, 1)
	reverseDone := make(chan struct{})
	go func() {
		defer close(reverseDone)
		var points int64
		err := reverseConn.QueryRow(context.WithoutCancel(ctx),
			`SELECT reverse_return_points($1)`, requestID).Scan(&points)
		reverseResultCh <- reverseResult{points: points, err: err}
	}()
	waitForBackendLock(t, reversePID, reverseDone)

	eraseResultCh := make(chan error, 1)
	eraseDone := make(chan struct{})
	go func() {
		defer close(eraseDone)
		_, err := eraseConn.Exec(context.WithoutCancel(ctx), `SELECT erase_user($1)`, userID)
		eraseResultCh <- err
	}()
	waitForBackendLock(t, erasePID, eraseDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release clawback barrier: %v", err)
	}
	select {
	case result := <-reverseResultCh:
		if result.err != nil || result.points != 120 {
			t.Fatalf("concurrent clawback = %d, %v; want 120 and no deadlock", result.points, result.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("clawback did not finish after its barrier was released")
	}
	select {
	case err := <-eraseResultCh:
		if err != nil {
			t.Fatalf("concurrent erasure: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("erasure did not finish after clawback committed")
	}

	var users, clawbacks int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM users WHERE id = $1),
		       (SELECT count(*) FROM loyalty_entries
		        WHERE return_request_id = $2 AND kind = 'clawback')`, userID, requestID).
		Scan(&users, &clawbacks); err != nil {
		t.Fatalf("read lock-order result: %v", err)
	}
	if users != 0 || clawbacks != 1 {
		t.Errorf("lock-order survivors users/clawbacks = %d/%d, want 0/1", users, clawbacks)
	}
}

func waitForBackendLock(t *testing.T, pid int, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatalf("backend %d finished before reaching the forced lock boundary", pid)
		default:
		}
		var waiting bool
		if err := pool.QueryRow(t.Context(), `
			SELECT coalesce(wait_event_type = 'Lock', false)
			FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&waiting); err != nil {
			t.Fatalf("observe backend %d: %v", pid, err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("backend %d never reached a lock wait", pid)
}

func assertReturnedLoyalty(t *testing.T, requestID, orderID, userID uuid.UUID, wantSpend, wantPoints int64) {
	t.Helper()
	ctx := t.Context()
	var spend int64
	if err := pool.QueryRow(ctx,
		`SELECT member_spend($1, $2, NULL)`, userID, loyalty.MembershipWindowDays).Scan(&spend); err != nil {
		t.Fatalf("read member spend: %v", err)
	}
	if spend != wantSpend {
		t.Errorf("member spend = %d, want %d", spend, wantSpend)
	}
	var points, requested int64
	var key string
	if err := pool.QueryRow(ctx, `
		SELECT points, requested_points, idempotency_key
		FROM loyalty_entries
		WHERE order_id = $1 AND kind = 'clawback'`, orderID).Scan(&points, &requested, &key); err != nil {
		t.Fatalf("read clawback: %v", err)
	}
	if points != wantPoints || requested != -wantPoints {
		t.Errorf("clawback points/requested = %d/%d, want %d/%d",
			points, requested, wantPoints, -wantPoints)
	}
	if want := "return:" + requestID.String(); key != want {
		t.Errorf("clawback key = %q, want %q", key, want)
	}
	var balance int64
	if err := pool.QueryRow(ctx, `
		SELECT b.points FROM loyalty_balances b
		JOIN store_credit_accounts a ON a.id = b.account_id
		WHERE a.user_id = $1`, userID).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if balance != 120+wantPoints {
		t.Errorf("points balance = %d, want %d", balance, 120+wantPoints)
	}
}

func couponedShippedOrder(t *testing.T, lines int) (requestID uuid.UUID, orderNumber string) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents, discount_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 8000, 50000
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	lineIDs := make([]uuid.UUID, 2)
	for i := range lineIDs {
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, $2, '測試商品', 50000, 1, $3) RETURNING id`,
			orderID, fmt.Sprintf("CPN-SKU-%d", i), i).Scan(&lineIDs[i]); err != nil {
			t.Fatalf("create line %d: %v", i, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'c@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	ref := "cs_cpn_" + orderNumber
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 58000)`, orderID, ref); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 58000, NULL, NULL)`, ref); err != nil {
		t.Fatalf("capture: %v", err)
	}
	moveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'black_cat', 'TC-'||$2) RETURNING id`, orderID, orderNumber).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason) VALUES ($1, '不合用') RETURNING id`,
		orderID).Scan(&requestID); err != nil {
		t.Fatalf("create return request: %v", err)
	}
	for i := range lineIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
			VALUES ($1, $2, $3, 1)`, orderID, shipmentID, lineIDs[i]); err != nil {
			t.Fatalf("create shipment line %d: %v", i, err)
		}
		if i < lines {
			if _, err := tx.Exec(ctx, `
				INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
				VALUES ($1, $2, $3, 1)`, orderID, requestID, lineIDs[i]); err != nil {
				t.Fatalf("create return line %d: %v", i, err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return requestID, orderNumber
}

func twoLineOrderForSequentialReturns(t *testing.T) (
	orderID uuid.UUID, orderNumber string, lineIDs [2]uuid.UUID,
) {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 15000
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	for i := range lineIDs {
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, $2, '運費退貨商品', 100000, 1, $3) RETURNING id`,
			orderID, fmt.Sprintf("RETURN-FEE-%d-%s", i, orderID), i).Scan(&lineIDs[i]); err != nil {
			t.Fatalf("create line %d: %v", i, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'return-fee@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	ref := "cs_return_fee_" + orderNumber
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 215000)`, orderID, ref); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 215000, NULL, NULL)`, ref); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	moveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'black_cat', 'RETURN-FEE-' || $2) RETURNING id`, orderID, orderNumber).
		Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	for i := range lineIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
			VALUES ($1, $2, $3, 1)`, orderID, shipmentID, lineIDs[i]); err != nil {
			t.Fatalf("ship line %d: %v", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return orderID, orderNumber, lineIDs
}

func TestARefundIsWhatTheCustomerPaid(t *testing.T) {
	tests := []struct {
		name  string
		lines int
		want  int64
		why   string
	}{
		{
			name: "one of two lines, so a proportional share of the coupon", lines: 1,
			want: 25000,
			why:  "the customer paid NT$250 for this item after the coupon, not NT$500",
		},
		{
			name: "both lines, so the whole contract and the delivery fee with it", lines: 2,
			want: 58000,
			why:  "a rescission returns everything paid under the contract, delivery included",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := staffContext(t)
			s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
			requestID, _ := couponedShippedOrder(t, tt.lines)

			if err := s.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err != nil {
				t.Fatalf("approve: %v — a return the shop cannot pay for is the defect", err)
			}
			var amount int64
			if err := pool.QueryRow(ctx, `
				SELECT amount_cents FROM refunds WHERE return_request_id = $1`,
				requestID).Scan(&amount); err != nil {
				t.Fatalf("read refund: %v", err)
			}
			if amount != tt.want {
				t.Errorf("refunded %d, want %d — %s", amount, tt.want, tt.why)
			}
		})
	}
}

func TestTheDeliveryFeeIsPaidBackOnce(t *testing.T) {
	ctx, _ := staffContext(t)
	orderID, number, lines := twoLineOrderForSequentialReturns(t)
	customerReturns := returns.NewStore(pool)
	shop := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	openAndApprove := func(reason string, lineID uuid.UUID) int64 {
		t.Helper()
		if err := customerReturns.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: reason, Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open %s return: %v", reason, err)
		}
		var requestID uuid.UUID
		if err := pool.QueryRow(ctx, `
			SELECT id FROM return_requests
			WHERE order_id = $1 AND status = 'requested'`, orderID).Scan(&requestID); err != nil {
			t.Fatalf("find %s return: %v", reason, err)
		}
		if err := shop.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("approve %s return: %v", reason, err)
		}
		var amount int64
		if err := pool.QueryRow(ctx,
			`SELECT amount_cents FROM refunds WHERE return_request_id = $1`, requestID).
			Scan(&amount); err != nil {
			t.Fatalf("read %s refund: %v", reason, err)
		}
		return amount
	}

	first := openAndApprove("first line", lines[0])
	if first != 100000 {
		t.Errorf("first line refunded %d, want 100000 without delivery", first)
	}
	second := openAndApprove("second line", lines[1])
	if second != 115000 {
		t.Errorf("second line refunded %d, want 115000 with delivery", second)
	}

	var captured, refunded int64
	if err := pool.QueryRow(ctx, `
		SELECT p.captured_amount_cents,
		       (SELECT coalesce(sum(r.amount_cents), 0)
		        FROM refunds r JOIN return_requests rr ON rr.id = r.return_request_id
		        WHERE rr.order_id = $1)
		FROM payments p WHERE p.order_id = $1 AND p.status = 'succeeded'`, orderID).
		Scan(&captured, &refunded); err != nil {
		t.Fatalf("read refund total: %v", err)
	}
	if captured != 215000 || refunded != captured {
		t.Errorf("captured=%d refunded=%d, want both 215000", captured, refunded)
	}
}

func TestApprovingAReturnRefundsWhatTheORDERSays(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	requestID, _ := returnedOrder(t, 1)

	if err := s.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("approve: %v", err)
	}

	var status string
	var amount int64
	var providerRef *string
	if err := pool.QueryRow(ctx, `
		SELECT status, amount_cents, provider_ref FROM refunds
		WHERE return_request_id = $1`, requestID).Scan(&status, &amount, &providerRef); err != nil {
		t.Fatalf("read refund: %v", err)
	}
	if status != "succeeded" {
		t.Errorf("refund status is %q, want succeeded", status)
	}
	if amount != 100000 {
		t.Errorf("refunded %d, want 100000 — one unit at the order's own price", amount)
	}
	if providerRef == nil || *providerRef == "" {
		t.Error("no provider reference recorded; the refund cannot be reconciled")
	}

	var returnStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
		t.Fatalf("read return: %v", err)
	}
	if returnStatus != "approved" {
		t.Errorf("return is %q, want approved", returnStatus)
	}
}

func TestAFailedRefundLeavesARowToReconcile(t *testing.T) {
	tests := []struct {
		name       string
		refunder   fakeRefunder
		wantStatus string
		why        string
	}{
		{
			name:       "stripe unreachable before anything was asked",
			refunder:   fakeRefunder{failIntent: true},
			wantStatus: "pending",
			why:        "goen never asked Stripe to refund, so the claim is still outstanding",
		},
		{
			name:       "the request timed out, so nobody knows what Stripe did",
			refunder:   fakeRefunder{refundErr: errors.New("context deadline exceeded")},
			wantStatus: "pending",
			why: "goen did not hear an answer, and 'failed' would claim the money " +
				"is still at the shop AND free the capture to be claimed twice",
		},
		{
			name: "stripe refused the refund",
			refunder: fakeRefunder{refundErr: fmt.Errorf("%w: %w",
				admin.ErrRefundCreateRejected,
				&stripe.Error{
					Type: stripe.ErrorTypeInvalidRequest,
					Code: stripe.ErrorCodeChargeAlreadyRefunded,
					Msg:  "charge has already been refunded",
				})},
			wantStatus: "failed",
			why:        "Stripe was asked and said no",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := staffContext(t)
			s := admin.NewStore(pool, tt.refunder, nil, nil)
			requestID, _ := returnedOrder(t, 2)

			if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err == nil {
				t.Fatal("a refund that did not happen was reported as success")
			}

			var status string
			var amount int64
			if err := pool.QueryRow(ctx, `
				SELECT status, amount_cents FROM refunds WHERE return_request_id = $1`,
				requestID).Scan(&status, &amount); err != nil {
				t.Fatalf("no refund row survives a failed provider call — nothing "+
					"can reconcile the money: %v", err)
			}
			if status != tt.wantStatus {
				t.Errorf("refund status is %q, want %q — %s", status, tt.wantStatus, tt.why)
			}
			if amount != 200000 {
				t.Errorf("row records %d, want 200000", amount)
			}

			var returnStatus string
			if err := pool.QueryRow(ctx,
				`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
				t.Fatalf("read return: %v", err)
			}
			// APPROVED. The decision commits before a cent moves, which is what
			// stops two staff members deciding one return at once from both
			// paying, so a refund that fails afterwards cannot reopen it. What
			// must be true instead is that the attempt is on record and can be
			// finished.
			if returnStatus != "approved" {
				t.Errorf("return is %q after a refund that did not happen, want "+
					"approved — the shop DID agree to the return, and the money "+
					"is what is outstanding", returnStatus)
			}
		})
	}
}

func TestAStalledRefundCanBeRetriedToCompletion(t *testing.T) {
	ctx, _ := staffContext(t)
	requestID, _ := returnedOrder(t, 2)

	stalled := admin.NewStore(pool, fakeRefunder{
		refundErr: errors.New("read tcp 1.2.3.4:443: i/o timeout"),
	}, nil, nil)

	if err := stalled.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err == nil {
		t.Fatal("a refund that timed out was reported as success")
	}

	healthy := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	if err := healthy.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("the retry was refused, so a stalled refund can never be finished "+
			"and the customer is never paid: %v", err)
	}

	var rows int
	var status string
	var succeededAt *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT count(*), min(status), min(succeeded_at) FROM refunds
		WHERE return_request_id = $1`, requestID).Scan(&rows, &status, &succeededAt); err != nil {
		t.Fatalf("read refunds: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d refund rows after a retry, want 1 — request_key exists so the "+
			"retry finds its own row rather than opening a second claim", rows)
	}
	if status != "succeeded" {
		t.Errorf("refund status is %q after a successful retry, want succeeded", status)
	}
	if succeededAt == nil {
		t.Error("no succeeded_at on a succeeded refund")
	}

	var returnStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
		t.Fatalf("read return: %v", err)
	}
	if returnStatus != "approved" {
		t.Errorf("return is %q after the refund finally went through, want approved", returnStatus)
	}
}

// TestReturnCompletionWaitsForExactPayoutAndClawback keeps an inspected parcel
// visible as approved work while its provider claim is ambiguous. Completion is
// admitted only after the same durable claim succeeds and the corresponding
// loyalty reversal is present; otherwise completed would hide every retry door.
func TestReturnCompletionWaitsForExactPayoutAndClawback(t *testing.T) {
	ctx, staff := staffContext(t)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, _, _ := loyaltyReturn(t, []int64{1200000}, 0)
	lineID := returnLineID(t, requestID)

	stalled := admin.NewStore(pool, fakeRefunder{
		refundErr: errors.New("provider outcome is ambiguous"),
	}, nil, nil)
	if err := stalled.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", actor); err == nil {
		t.Fatal("ambiguous provider refund was reported as settled")
	}
	if err := stalled.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: lineID, Received: 1, Restocked: 0,
	}}, actor); err != nil {
		t.Fatalf("inspect return before payout retry: %v", err)
	}

	err := stalled.CompleteReturn(ctx, requestID.String(), "已驗貨", actor)
	if !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("complete with ambiguous payout = %v, want ErrRefused", err)
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "return_requests_completed_money_settled" {
		t.Fatalf("incomplete payout refused by %v, want return_requests_completed_money_settled", err)
	}

	healthy := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	if err := healthy.Decide(ctx, requestID.String(), "approved", "完成退款", "", actor); err != nil {
		t.Fatalf("retry approved payout and clawback: %v", err)
	}
	if err := healthy.CompleteReturn(ctx, requestID.String(), "已退款並驗貨", actor); err != nil {
		t.Fatalf("complete exactly settled return: %v", err)
	}

	var status string
	var succeededRefunds, clawbacks int
	if err := pool.QueryRow(ctx, `
		SELECT r.status,
		       (SELECT count(*) FROM refunds rf
		        WHERE rf.return_request_id = r.id AND rf.status = 'succeeded'),
		       (SELECT count(*) FROM loyalty_entries e
		        WHERE e.return_request_id = r.id AND e.kind = 'clawback')
		FROM return_requests r WHERE r.id = $1`, requestID).
		Scan(&status, &succeededRefunds, &clawbacks); err != nil {
		t.Fatalf("read completed return settlement: %v", err)
	}
	if status != "completed" || succeededRefunds != 1 || clawbacks != 1 {
		t.Errorf("completed settlement status/refunds/clawbacks = %s/%d/%d, want completed/1/1",
			status, succeededRefunds, clawbacks)
	}
}

func TestAStalledRefundOffersItsRetryInTheQueue(t *testing.T) {
	ctx := t.Context()
	isolated := dbtest.Pool(t)
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatalf("read isolated stalled-refund seed: %v", err)
	}
	if _, execErr := isolated.Exec(ctx, string(seed)); execErr != nil {
		t.Fatalf("load isolated stalled-refund seed: %v", execErr)
	}

	for _, tc := range []struct {
		name            string
		refunder        fakeRefunder
		wantOutstanding bool
		wantBlocked     bool
	}{
		{
			name: "an ambiguous transport failure remains retryable",
			refunder: fakeRefunder{
				refundErr: errors.New("read tcp 1.2.3.4:443: i/o timeout"),
			},
			wantOutstanding: true,
		},
		{
			name:            "a provider refusal offers a successor",
			refunder:        fakeRefunder{state: admin.RefundFailed},
			wantOutstanding: true,
		},
		{
			name:     "a settled payout offers nothing twice",
			refunder: fakeRefunder{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			staffCtx, _ := staffContextOn(t, isolated)
			requestID, _ := returnedOrderOn(t, isolated, 1)
			s := admin.NewStore(isolated, tc.refunder, nil, nil)
			_ = s.Decide(staffCtx, requestID.String(), "approved", "退款", "", uuid.NullUUID{})

			view, err := s.Returns(staffCtx)
			if err != nil {
				t.Fatalf("read queue: %v", err)
			}
			var found *adminpages.Return
			for i := range view.Rows {
				if view.Rows[i].ID == requestID.String() {
					found = &view.Rows[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("return %s is absent from queue", requestID)
			}
			if !found.Decided {
				t.Error("the committed approval is rendered as undecided")
			}
			if found.PayoutOutstanding != tc.wantOutstanding || found.PayoutBlocked != tc.wantBlocked {
				t.Errorf("payout flags = outstanding %v blocked %v, want %v/%v",
					found.PayoutOutstanding, found.PayoutBlocked,
					tc.wantOutstanding, tc.wantBlocked)
			}
		})
	}

	t.Run("a split payout offers the half that has not landed", func(t *testing.T) {
		staffCtx, _ := staffContextOn(t, isolated)
		requestID, orderNumber, _ := creditFundedReturnOn(t, isolated, 2, 60000)
		if _, err := isolated.Exec(staffCtx, `
			UPDATE return_requests SET status = 'approved', decided_at = now(), resolution = '退款'
			WHERE id = $1`, requestID); err != nil {
			t.Fatalf("approve split return: %v", err)
		}
		if _, err := isolated.Exec(staffCtx, `
			INSERT INTO refunds (payment_id, request_key, amount_cents, reason,
			                     return_request_id, status, provider_ref, succeeded_at)
			SELECT p.id, 'return:' || $1::text, 140000, '退款', $1::uuid,
			       'succeeded', 're_queue_split', now()
			FROM payments p JOIN orders o ON o.id = p.order_id
			WHERE o.order_number = $2 AND p.status = 'succeeded'`, requestID, orderNumber); err != nil {
			t.Fatalf("construct settled card half: %v", err)
		}
		s := admin.NewStore(isolated, fakeRefunder{}, nil, nil)
		view, err := s.Returns(staffCtx)
		if err != nil {
			t.Fatalf("read split queue: %v", err)
		}
		for i := range view.Rows {
			if view.Rows[i].ID == requestID.String() {
				if !view.Rows[i].PayoutOutstanding || view.Rows[i].PayoutBlocked {
					t.Errorf("split payout flags = outstanding %v blocked %v, want true/false",
						view.Rows[i].PayoutOutstanding, view.Rows[i].PayoutBlocked)
				}
				return
			}
		}
		t.Fatalf("split return %s is absent from queue", requestID)
	})
}

// TestAnOldRecoverySurvivesTheBoundedReturnQueue proves ordering happens before
// LIMIT. The returns page is the only retry door; if fifty newer intake rows can
// hide an approved-but-unpaid return, that customer can remain unpaid forever.
func TestAnOldRecoverySurvivesTheBoundedReturnQueue(t *testing.T) {
	ctx, _ := staffContext(t)
	requestID, _ := returnedOrder(t, 1)
	s := admin.NewStore(pool, fakeRefunder{state: admin.RefundFailed}, nil, nil)
	decideErr := s.Decide(ctx, requestID.String(), "approved", "terminal retry", "", uuid.NullUUID{})
	if !errors.Is(decideErr, admin.ErrRefundIncomplete) || errors.Is(decideErr, admin.ErrRefused) {
		t.Fatalf("terminal decision = %v, want only ErrRefundIncomplete", decideErr)
	}

	rows, err := pool.Query(ctx, `
		WITH method AS (
			SELECT v.id, sm.code, v.name
			FROM shipping_method_versions v
			JOIN shipping_methods sm ON sm.id = v.method_id
			ORDER BY v.effective_at
			LIMIT 1
		), crowded_orders AS (
			INSERT INTO orders (
				shipping_version_id, shipping_method_code, shipping_method_name
			)
			SELECT m.id, m.code, m.name
			FROM method m CROSS JOIN generate_series(1, $1::integer)
			RETURNING id
		), crowded_lines AS (
			INSERT INTO order_lines
				(order_id, sku, product_name, unit_price_cents, quantity)
			SELECT id, 'QUEUE-' || id::text, 'queue fixture', 10000, 1
			FROM crowded_orders
			RETURNING order_id
		), crowded_private_data AS (
			INSERT INTO order_private_data
				(order_id, email, recipient_name, phone, postal_code, city, district, street)
			SELECT id, 'queue+' || id::text || '@example.invalid', 'queue fixture',
			       '0912345678', '110', '台北市', '信義區', '測試路 1 號'
			FROM crowded_orders
			RETURNING order_id
		), crowded_returns AS (
			INSERT INTO return_requests (order_id, reason)
			SELECT l.order_id, 'newer queue intake'
			FROM crowded_lines l
			JOIN crowded_private_data p USING (order_id)
			RETURNING order_id
		)
		SELECT order_id FROM crowded_returns`, admin.PageSize+5)
	if err != nil {
		t.Fatalf("create bounded-queue crowd: %v", err)
	}
	var crowdedOrders []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if scanErr := rows.Scan(&id); scanErr != nil {
			rows.Close()
			t.Fatalf("scan crowded order: %v", scanErr)
		}
		crowdedOrders = append(crowdedOrders, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		t.Fatalf("iterate crowded orders: %v", rowsErr)
	}
	rows.Close()
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		for _, query := range []string{
			`DELETE FROM return_requests WHERE order_id = ANY($1::uuid[])`,
			`DELETE FROM order_private_data WHERE order_id = ANY($1::uuid[])`,
			`DELETE FROM order_lines WHERE order_id = ANY($1::uuid[])`,
			`DELETE FROM orders WHERE id = ANY($1::uuid[])`,
		} {
			if _, cleanupErr := pool.Exec(cleanupCtx, query, crowdedOrders); cleanupErr != nil {
				t.Errorf("remove bounded-queue crowd: %v", cleanupErr)
			}
		}
	})

	queue, err := s.Returns(ctx)
	if err != nil {
		t.Fatalf("read bounded return queue: %v", err)
	}
	if len(queue.Rows) != admin.PageSize {
		t.Fatalf("bounded queue has %d rows, want %d", len(queue.Rows), admin.PageSize)
	}
	for i := range queue.Rows {
		if queue.Rows[i].ID != requestID.String() {
			continue
		}
		if !queue.Rows[i].CanRetryPayout() {
			t.Fatalf("old recovery row at position %d has outstanding/blocked %v/%v, want a retry",
				i, queue.Rows[i].PayoutOutstanding, queue.Rows[i].PayoutBlocked)
		}
		return
	}
	t.Fatalf("approved recovery %s was hidden behind %d newer intake rows",
		requestID, len(crowdedOrders))
}

func TestTerminalRefundHTTPUsesThePayoutRecoveryNotice(t *testing.T) {
	ctx, _ := staffContext(t)
	requestID, _ := returnedOrder(t, 1)
	h := adminHandlerOver(pool,
		admin.NewStore(pool, fakeRefunder{state: admin.RefundCancelled}, nil, nil))
	form := url.Values{
		"decision":   {"approved"},
		"confirm":    {"approved"},
		"resolution": {"provider cancelled"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/returns/"+requestID.String()+"/decide", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", requestID.String())
	res := httptest.NewRecorder()

	h.Decide(res, req)
	if res.Code != http.StatusSeeOther ||
		res.Header().Get("Location") != "/admin/returns?refundfailed=1" {
		t.Fatalf("terminal refund HTTP = %d %q, want payout-recovery redirect",
			res.Code, res.Header().Get("Location"))
	}
}

func TestReturnResolutionOverTheDurableBoundIsRefusedBeforeDecision(t *testing.T) {
	ctx, _ := staffContext(t)
	requestID, _ := returnedOrder(t, 1)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	tooLong := strings.Repeat("界", 301)
	if err := s.Decide(ctx, requestID.String(), "approved", tooLong, "", uuid.NullUUID{}); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("overlong Store resolution = %v, want ErrInvalid", err)
	}

	form := url.Values{"decision": {"approved"}, "confirm": {"approved"}, "resolution": {tooLong}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/returns/"+requestID.String()+"/decide", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", requestID.String())
	res := httptest.NewRecorder()
	adminHandlerOver(pool, s).Decide(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/returns?refused=1" {
		t.Fatalf("overlong resolution HTTP = %d %q, want refused redirect",
			res.Code, res.Header().Get("Location"))
	}

	var status string
	var attempts int
	if err := pool.QueryRow(ctx, `
		SELECT r.status,
		       (SELECT count(*) FROM refunds rf WHERE rf.return_request_id = r.id)
		FROM return_requests r WHERE r.id = $1`, requestID).Scan(&status, &attempts); err != nil {
		t.Fatalf("read return after overlong resolution: %v", err)
	}
	if status != "requested" || attempts != 0 {
		t.Errorf("overlong resolution left status/attempts = %s/%d, want requested/0", status, attempts)
	}
}

func TestAPendingProviderRefundIsNotRecordedAsSucceeded(t *testing.T) {
	tests := []struct {
		name  string
		state admin.RefundState
	}{
		{name: "stripe accepted it and has not settled it", state: admin.RefundPending},
		{name: "stripe needs something else to happen first", state: admin.RefundRequiresAction},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := staffContext(t)
			s := admin.NewStore(pool, fakeRefunder{state: tt.state}, nil, nil)
			requestID, _ := returnedOrder(t, 1)

			if err := s.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err != nil {
				t.Fatalf("a refund Stripe ACCEPTED was treated as a failure: %v", err)
			}

			var status string
			var succeededAt, failedAt *time.Time
			var providerRef *string
			if err := pool.QueryRow(ctx, `
				SELECT status, succeeded_at, failed_at, provider_ref FROM refunds
				WHERE return_request_id = $1`, requestID).
				Scan(&status, &succeededAt, &failedAt, &providerRef); err != nil {
				t.Fatalf("read refund: %v", err)
			}
			if status != string(tt.state) {
				t.Errorf("refund status is %q, want %q — goen recorded a state the "+
					"provider never claimed", status, tt.state)
			}
			if succeededAt != nil {
				t.Errorf("succeeded_at is %v on a refund that has not succeeded", *succeededAt)
			}
			if failedAt != nil {
				t.Errorf("failed_at is %v on a refund that has not failed", *failedAt)
			}
			if providerRef == nil || *providerRef == "" {
				t.Error("no provider reference on a refund Stripe accepted — it is " +
					"the only handle anybody has for chasing it")
			}

			var returnStatus string
			if err := pool.QueryRow(ctx,
				`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
				t.Fatalf("read return: %v", err)
			}
			if returnStatus != "approved" {
				t.Errorf("return is %q, want approved — the shop accepted the goods back", returnStatus)
			}
			var refundEvents int
			if err := pool.QueryRow(ctx, `
				SELECT count(*) FROM order_events e
				JOIN return_requests r ON r.order_id = e.order_id
				WHERE r.id = $1 AND e.kind = 'refunded'`, requestID).Scan(&refundEvents); err != nil {
				t.Fatalf("count events: %v", err)
			}
			if refundEvents != 0 {
				t.Errorf("%d 'refunded' events on an order whose refund has not landed — "+
					"the customer reads that timeline", refundEvents)
			}
		})
	}
}

// TestAProviderRefusalGetsANewDurableAttempt holds the distinction between
// retrying ambiguity and retrying a known terminal outcome. Ambiguity reuses one
// provider key; failed/cancelled is immutable evidence and gets a linked next
// generation with a fresh DB-derived key.
func TestAProviderRefusalGetsANewDurableAttempt(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{state: admin.RefundFailed}, nil, nil)
	requestID, _ := returnedOrder(t, 1)

	if err := s.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err == nil {
		t.Fatal("a refund Stripe refused was reported as success")
	}

	var returnStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
		t.Fatalf("read return: %v", err)
	}
	if returnStatus != "approved" {
		t.Errorf("return is %q, want approved — the decision is taken before any "+
			"money moves, which is what stops two staff members both paying",
			returnStatus)
	}

	// On record, so nothing is lost while it is outstanding.
	var refundStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM refunds WHERE return_request_id = $1`, requestID).Scan(&refundStatus); err != nil {
		t.Fatalf("no refund row survives a refusal, so nothing can reconcile it: %v", err)
	}
	if refundStatus != "failed" {
		t.Errorf("refund is %q, want failed — Stripe was asked and said no", refundStatus)
	}

	healthy := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	if err := healthy.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("retry after a known provider refusal: %v", err)
	}

	type attempt struct {
		id       uuid.UUID
		previous uuid.NullUUID
		number   int32
		key      string
		status   string
	}
	rows, err := pool.Query(ctx, `
		SELECT id, previous_refund_id, attempt_no, request_key, status
		FROM refunds WHERE return_request_id = $1 ORDER BY attempt_no`, requestID)
	if err != nil {
		t.Fatalf("read provider attempts: %v", err)
	}
	defer rows.Close()
	var attempts []attempt
	for rows.Next() {
		var got attempt
		if scanErr := rows.Scan(&got.id, &got.previous, &got.number, &got.key, &got.status); scanErr != nil {
			t.Fatalf("scan provider attempt: %v", scanErr)
		}
		attempts = append(attempts, got)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("iterate provider attempts: %v", rowsErr)
	}
	base := "return:" + requestID.String()
	if len(attempts) != 2 {
		t.Fatalf("provider attempts = %#v, want failed evidence plus one successor", attempts)
	}
	if attempts[0].number != 1 || attempts[0].key != base || attempts[0].status != "failed" ||
		attempts[0].previous.Valid {
		t.Errorf("first provider attempt = %#v, want immutable failed generation 1", attempts[0])
	}
	if attempts[1].number != 2 || attempts[1].key != base+":attempt:2" ||
		attempts[1].status != "succeeded" || !attempts[1].previous.Valid ||
		attempts[1].previous.UUID != attempts[0].id {
		t.Errorf("second provider attempt = %#v, want succeeded generation 2 linked to %s",
			attempts[1], attempts[0].id)
	}
	page, err := health.NewStore(pool).WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("read health after successful successor: %v", err)
	}
	for i := range page.OpenRefunds {
		if strings.HasPrefix(page.OpenRefunds[i].Key, base) {
			t.Errorf("historical failed attempt still appears as current health work: %#v",
				page.OpenRefunds[i])
		}
	}
}

func TestTheHealthPageNamesARefundThatDidNotLand(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{
		refundErr: errors.New("read tcp 1.2.3.4:443: i/o timeout"),
	}, nil, nil)

	requestID, orderNumber := returnedOrder(t, 1)

	if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err == nil {
		t.Fatal("a refund that timed out was reported as success")
	}

	view, err := health.NewStore(pool).WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if view.RefundsHealthy() {
		t.Error("a refund that never left reads as healthy")
	}

	// Found by identity, never by position: the suite is shuffled and other tests leave refunds behind.
	var found *adminpages.OpenRefund
	for i := range view.OpenRefunds {
		if view.OpenRefunds[i].Key == "return:"+requestID.String() {
			found = &view.OpenRefunds[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("the stalled refund for return %s is on no page — the row exists "+
			"only for reconciliation and nothing can read it", requestID)
	}
	want := adminpages.OpenRefund{
		OrderNumber: orderNumber,
		Key:         "return:" + requestID.String(),
		Status:      "pending",
		AmountCents: 100000,
		ProviderRef: "",
		Since:       found.Since,
	}
	if diff := cmp.Diff(want, *found); diff != "" {
		t.Errorf("WorkerHealth() open refund mismatch (-want +got):\n%s", diff)
	}
}

func TestRefundHealthCountExceedsItsBoundedDiagnosticSample(t *testing.T) {
	ctx, _ := staffContext(t)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))
	before, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("read refund count before sample crowd: %v", err)
	}
	_, orderNumber := returnedOrder(t, 1)
	prefix := "health-count-" + uuid.NewString() + "-"
	if _, insertErr := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents)
		SELECT p.id, $2 || g::text, 1
		FROM payments p
		JOIN orders o ON o.id = p.order_id
		CROSS JOIN generate_series(1, 25) g
		WHERE o.order_number = $1 AND p.status = 'succeeded'`, orderNumber, prefix); insertErr != nil {
		t.Fatalf("create open-refund sample crowd: %v", insertErr)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, cleanupErr := pool.Exec(cleanupCtx,
			`DELETE FROM refunds WHERE request_key LIKE $1 || '%'`, prefix); cleanupErr != nil {
			t.Errorf("remove open-refund sample crowd: %v", cleanupErr)
		}
	})

	after, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("read refund count after sample crowd: %v", err)
	}
	if after.OpenRefundCount != before.OpenRefundCount+25 {
		t.Errorf("exact open refund count moved %d -> %d, want +25",
			before.OpenRefundCount, after.OpenRefundCount)
	}
	if len(after.OpenRefunds) != health.OpenRefundListLimit {
		t.Errorf("diagnostic sample has %d rows, want bounded %d",
			len(after.OpenRefunds), health.OpenRefundListLimit)
	}
	enCtx := i18n.WithLocale(ctx, i18n.En)
	wantText := fmt.Sprintf(i18n.T(enCtx, i18n.KeyHealthRefundsStuck), after.OpenRefundCount)
	if got := after.RefundsText(enCtx); got != wantText {
		t.Errorf("refund health text = %q, want exact count %q", got, wantText)
	}
}

func TestRejectingAReturnMovesNoMoney(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	requestID, _ := returnedOrder(t, 2)

	if err := s.Decide(ctx, requestID.String(), "rejected", "超過鑑賞期", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("reject: %v", err)
	}

	var refunds int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refunds WHERE return_request_id = $1`, requestID).Scan(&refunds); err != nil {
		t.Fatalf("count: %v", err)
	}
	if refunds != 0 {
		t.Errorf("%d refunds written for a REJECTED return", refunds)
	}

	var status, resolution string
	if err := pool.QueryRow(ctx,
		`SELECT status, coalesce(resolution, '') FROM return_requests WHERE id = $1`,
		requestID).Scan(&status, &resolution); err != nil {
		t.Fatalf("read return: %v", err)
	}
	if status != "rejected" || resolution != "超過鑑賞期" {
		t.Errorf("return is %q/%q, want rejected and the reason the staff gave", status, resolution)
	}
}

func TestAReturnIsDecidedOnce(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	requestID, _ := returnedOrder(t, 1)

	if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
		t.Errorf("second decision gave %v, want ErrRefused", err)
	}

	var refunds int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refunds WHERE return_request_id = $1`, requestID).Scan(&refunds); err != nil {
		t.Fatalf("count: %v", err)
	}
	if refunds != 1 {
		t.Errorf("%d refunds after two approvals, want 1", refunds)
	}
}

func TestTheLoserOfTwoSimultaneousDecisionsWritesNoAuditRow(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	requestID, _ := returnedOrder(t, 1)

	before := auditRowsFor(t, requestID)

	t1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin T1: %v", err)
	}
	defer func() { _ = t1.Rollback(ctx) }()
	if _, err := t1.Exec(ctx, `
		UPDATE return_requests SET status = 'rejected', decided_at = now()
		WHERE id = $1 AND status = 'requested'`, requestID); err != nil {
		t.Fatalf("T1 decide: %v", err)
	}

	decided := make(chan error, 1)
	go func() { decided <- s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}) }()

	select {
	case err := <-decided:
		t.Fatalf("T2 finished before T1 committed (%v); it never met the lock, "+
			"so this run proves nothing", err)
	case <-time.After(250 * time.Millisecond):
	}

	if err := t1.Commit(ctx); err != nil {
		t.Fatalf("commit T1: %v", err)
	}

	if err := <-decided; !errors.Is(err, admin.ErrRefused) {
		t.Errorf("the second decision returned %v, want ErrRefused — it updated no "+
			"row and reported success", err)
	}

	if after := auditRowsFor(t, requestID); after != before {
		t.Errorf("%d audit rows for this return, was %d — the losing decision was "+
			"recorded as though it had been made", after, before)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "rejected" {
		t.Errorf("the return is %q, want rejected — the loser overwrote the winner", status)
	}

	// THE MONEY. Every assertion above stays true even when the loser refunds
	// before losing the CAS: a count proves the database held the line, and only
	// this says whether a cent moved.
	var refunds int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refunds WHERE return_request_id = $1`, requestID).Scan(&refunds); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	if refunds != 0 {
		t.Errorf("%d refunds against a return that was REJECTED — the losing "+
			"decision paid before it found out it had lost", refunds)
	}
}

func auditRowsFor(t *testing.T, requestID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM audit_events
		WHERE entity_table = 'return_requests' AND entity_id = $1`, requestID).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

func TestARefundCannotExceedWhatWasCaptured(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	requestID, orderNumber := returnedOrder(t, 2)

	if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("first approval: %v", err)
	}

	// Written straight in: the return ceiling would refuse a second request before the refund guard.
	var orderID, lineID, second uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT o.id, ol.id FROM orders o JOIN order_lines ol ON ol.order_id = o.id
		WHERE o.order_number = $1`, orderNumber).Scan(&orderID, &lineID); err != nil {
		t.Fatalf("find order: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason) VALUES ($1, '再退一次') RETURNING id`,
		orderID).Scan(&second); err != nil {
		t.Fatalf("create second return: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 2)`, orderID, second, lineID); err == nil {
		if decideErr := s.Decide(ctx, second.String(), "approved", "", "", uuid.NullUUID{}); decideErr == nil {
			t.Fatal("the same order was refunded twice")
		}
	}

	var total int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(r.amount_cents), 0) FROM refunds r
		JOIN payments p ON p.id = r.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND r.status IN ('pending', 'succeeded')`,
		orderNumber).Scan(&total); err != nil {
		t.Fatalf("sum refunds: %v", err)
	}
	if total > 200000 {
		t.Errorf("%d refunded against a capture of 200000", total)
	}
}

func TestTheBackOfficeIsInvisibleToEveryoneButStaff(t *testing.T) {
	ctx := t.Context()

	var customerID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role) VALUES ('shopper-'||gen_random_uuid()||'@example.com', 'customer')
		RETURNING id`).Scan(&customerID); err != nil {
		t.Fatalf("create customer: %v", err)
	}

	guarded := backOffice.RequireStaff(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("the back office"))
	})

	tests := []struct {
		name     string
		signedIn bool
		user     account.User
	}{
		{name: "signed out"},
		{name: "a signed-in customer", signedIn: true,
			user: account.User{ID: customerID.String(), Role: "customer"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin", nil)
			if tt.signedIn {
				req = req.WithContext(account.WithUser(req.Context(), tt.user))
			}
			w := httptest.NewRecorder()
			guarded(w, req)

			if w.Code != http.StatusNotFound {
				t.Errorf("status is %d, want 404 — anything else says /admin is a "+
					"real place", w.Code)
			}
			if loc := w.Header().Get("Location"); loc != "" {
				t.Errorf("redirected to %q; a redirect confirms the page exists and "+
					"puts its path in the visitor's history", loc)
			}
			if strings.Contains(w.Body.String(), "the back office") {
				t.Error("the handler ran")
			}
		})
	}

	// BOTH back-office roles, and 'staff' is the one that matters: a colleague
	// hired as staff must not meet a 404 on the whole back office.
	staffView, err := roster.NewStore(pool).Staff(ctx)
	if err != nil {
		t.Fatalf("read roles offered by /admin/staff: %v", err)
	}
	for _, offered := range staffView.Roles {
		role := string(offered)
		t.Run(role+" reaches the back office", func(t *testing.T) {
			var id uuid.UUID
			if err := pool.QueryRow(ctx, `
				INSERT INTO users (email, role) VALUES ('bo-'||gen_random_uuid()||'@example.com', $1)
				RETURNING id`, role).Scan(&id); err != nil {
				t.Fatalf("create %s: %v", role, err)
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin", nil)
			req = req.WithContext(account.WithUser(req.Context(),
				account.User{ID: id.String(), Role: account.Role(role)}))
			w := httptest.NewRecorder()
			guarded(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("%s got %d, want 200 — /admin/staff offers this role, so a "+
					"colleague hired into it can do no work at all", role, w.Code)
			}
		})
	}
}

func TestOnlyAnAdminReachesTheStaffPage(t *testing.T) {
	ctx := t.Context()

	guarded := backOffice.RequireAdmin(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("who works here"))
	})

	newUser := func(role string) account.User {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (email, role)
			VALUES ('`+role+`-'||gen_random_uuid()||'@example.com', $1)
			RETURNING id`, role).Scan(&id); err != nil {
			t.Fatalf("create %s: %v", role, err)
		}
		return account.User{ID: id.String(), Role: account.Role(role)}
	}

	for _, tt := range []struct {
		name     string
		signedIn bool
		user     account.User
		want     int
	}{
		{name: "signed out", want: http.StatusNotFound},
		{name: "a customer", signedIn: true, user: newUser("customer"), want: http.StatusNotFound},
		{name: "a staff member", signedIn: true, user: newUser("staff"), want: http.StatusNotFound},
		{name: "an admin", signedIn: true, user: newUser("admin"), want: http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/staff", nil)
			if tt.signedIn {
				req = req.WithContext(account.WithUser(req.Context(), tt.user))
			}
			w := httptest.NewRecorder()
			guarded(w, req)

			if w.Code != tt.want {
				t.Errorf("status is %d, want %d", w.Code, tt.want)
			}
			ran := strings.Contains(w.Body.String(), "who works here")
			if want := tt.want == http.StatusOK; ran != want {
				t.Errorf("the handler ran = %v, want %v", ran, want)
			}
		})
	}
}

func staffContext(t *testing.T) (context.Context, uuid.UUID) {
	t.Helper()
	return staffContextOn(t, pool)
}

func staffContextOn(t *testing.T, p *pgxpool.Pool) (context.Context, uuid.UUID) {
	t.Helper()
	return admintest.StaffContext(t, p)
}

func auditRows(t *testing.T, action audit.Action) int {
	t.Helper()
	return admintest.AuditRows(t, pool, action)
}

func TestEveryBackOfficeWriteLeavesATrail(t *testing.T) {
	ctx, actor := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	sku := anyVariantSKU(t)
	slug := anyProductSlug(t)

	tests := []struct {
		name   string
		action audit.Action
		run    func() error
	}{
		{"adjust stock", audit.ActionAdjustStock, func() error {
			return stock.NewStore(pool).Adjust(ctx, sku, 3, actor.String(), uuid.NewString())
		}},
		{"reprice", audit.ActionRepriceVariant, func() error {
			return stock.NewStore(pool).SetPrice(ctx, sku, 123400, 0)
		}},
		{"publish", audit.ActionPublishProduct, func() error {
			return s.SetProductStatus(ctx, slug, "draft")
		}},
		{"grant credit", audit.ActionGrantCredit, func() error {
			_, grantErr := loyalty.NewStore(pool).GrantCredit(ctx, actor, 500,
				"測試", uuid.New())
			return grantErr
		}},
		{"create campaign", audit.ActionCreateCampaign, func() error {
			_, err := campaigns.NewStore(pool).Create(ctx, &campaigns.Form{
				Slug: "trail-" + uuid.NewString()[:8], Title: "紀錄", Days: 7,
			})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := auditRows(t, tt.action)
			if err := tt.run(); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if after := auditRows(t, tt.action); after != before+1 {
				t.Errorf("%s left %d audit rows, want one more than %d — the action "+
					"happened and nobody can say who did it", tt.name, after, before)
			}
		})
	}
}

func TestAnAuditRowNamesItsActorAndRequest(t *testing.T) {
	ctx, actor := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	if err := s.SetProductStatus(ctx, anyProductSlug(t), "draft"); err != nil {
		t.Fatalf("publish: %v", err)
	}

	var gotActor uuid.UUID
	var requestID, action string
	if err := pool.QueryRow(ctx, `
		SELECT actor_user_id, coalesce(request_id, ''), action FROM audit_events
		WHERE actor_user_id = $1 ORDER BY occurred_at DESC LIMIT 1`, actor).
		Scan(&gotActor, &requestID, &action); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if gotActor != actor {
		t.Errorf("actor is %s, want %s", gotActor, actor)
	}
	if requestID == "" {
		t.Error("no request id; the row cannot be put beside the log lines from " +
			"the same request")
	}
	if action != string(audit.ActionPublishProduct) {
		t.Errorf("action is %q", action)
	}
}

func TestProductUpdateAndAuditCommitTogether(t *testing.T) {
	ctx, actor := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := draftProduct(t, ctx, s)
	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("read product fixture: %v", err)
	}
	form := &admin.ProductForm{
		Slug: slug, Name: "已稽核商品 " + uuid.NewString()[:8], Summary: view.Summary,
		Description: view.Description, NameEn: view.NameEn,
		SummaryEn: view.SummaryEn, DescriptionEn: view.DescriptionEn,
		WarrantyNote: view.WarrantyNote, WarrantyMonths: view.WarrantyMonths,
		BrandID: view.BrandID, CategoryID: view.CategoryID,
	}
	before := auditRows(t, audit.ActionUpdateProduct)
	if errs, updateErr := s.UpdateProduct(ctx, form); updateErr != nil || len(errs) > 0 {
		t.Fatalf("UpdateProduct: %v %v", updateErr, errs)
	}

	var gotActor uuid.UUID
	var requestID, auditedSlug, auditedName string
	if err := pool.QueryRow(ctx, `
		SELECT actor_user_id, coalesce(request_id, ''),
		       coalesce(after->>'slug', ''), coalesce(after->>'name', '')
		FROM audit_events
		WHERE action = $1 AND after->>'slug' = $2
		ORDER BY occurred_at DESC, id DESC LIMIT 1`,
		string(audit.ActionUpdateProduct), slug).
		Scan(&gotActor, &requestID, &auditedSlug, &auditedName); err != nil {
		t.Fatalf("read product update audit row: %v", err)
	}
	if gotActor != actor || requestID == "" || auditedSlug != slug || auditedName != form.Name {
		t.Errorf("product update audit = actor %s request %q slug %q name %q; "+
			"want %s/nonempty/%q/%q", gotActor, requestID, auditedSlug, auditedName,
			actor, slug, form.Name)
	}
	if after := auditRows(t, audit.ActionUpdateProduct); after != before+1 {
		t.Fatalf("successful update left %d audit rows, want %d", after, before+1)
	}

	// A syntactically valid but nonexistent actor reaches record_audit_event and
	// fails its users foreign key. The product write ran first in the same
	// transaction, so observing the old name proves the audit failure rolled it
	// back instead of leaving an unattributed customer-visible change.
	missingActor := uuid.New()
	failingCtx := web.WithRequestID(account.WithUser(t.Context(), account.User{
		ID: missingActor.String(), Role: "admin",
	}), "req-missing-actor")
	failing := *form
	failing.Name = "不得落地 " + uuid.NewString()[:8]
	errs, updateErr := s.UpdateProduct(failingCtx, &failing)
	if updateErr == nil || len(errs) > 0 {
		t.Fatalf("UpdateProduct with an unrecordable actor = %v, %v; want audit error",
			errs, updateErr)
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](updateErr)
	if !ok || pgErr.ConstraintName != "audit_events_actor_user_id_fkey" {
		t.Fatalf("product audit insertion failure = %v, want audit actor FK", updateErr)
	}
	var persisted string
	if err := pool.QueryRow(ctx, `SELECT name FROM products WHERE slug = $1`, slug).Scan(&persisted); err != nil {
		t.Fatalf("read product after audit failure: %v", err)
	}
	if persisted != form.Name {
		t.Errorf("product name after audit failure = %q, want rolled back to %q",
			persisted, form.Name)
	}
	if after := auditRows(t, audit.ActionUpdateProduct); after != before+1 {
		t.Errorf("failed audit changed product-update trail from %d to %d", before+1, after)
	}
}

func TestAnActionWithNoActorIsRefused(t *testing.T) {
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := anyProductSlug(t)

	// Read before and compared after: anyProductSlug can hand back a product already in the target status.
	var before string
	if scanErr := pool.QueryRow(t.Context(),
		`SELECT status FROM products WHERE slug = $1`, slug).Scan(&before); scanErr != nil {
		t.Fatalf("read product: %v", scanErr)
	}
	target := "draft"
	if before == "draft" {
		target = "archived"
	}

	err := s.SetProductStatus(t.Context(), slug, target)
	if !errors.Is(err, audit.ErrNoActor) {
		t.Fatalf("a back-office write with no actor gave %v, want ErrNoActor", err)
	}

	var after string
	if scanErr := pool.QueryRow(t.Context(),
		`SELECT status FROM products WHERE slug = $1`, slug).Scan(&after); scanErr != nil {
		t.Fatalf("read product: %v", scanErr)
	}
	if after != before {
		t.Errorf("the write landed anyway (%s → %s); the audit failure must roll it back",
			before, after)
	}
}

func TestAFailedWriteLeavesNoAuditRow(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	before := auditRows(t, audit.ActionPublishProduct)
	if err := s.SetProductStatus(ctx, anyProductSlug(t), "nonsense"); err == nil {
		t.Fatal("an invalid status was accepted")
	}
	if err := s.SetProductStatus(ctx, "no-such-product-"+uuid.NewString(), "active"); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("publishing an absent product gave %v, want ErrNotFound", err)
	}
	if after := auditRows(t, audit.ActionPublishProduct); after != before {
		t.Errorf("%d audit rows after a refused write, want %d", after, before)
	}
}

func TestTheTrailCannotBeRewritten(t *testing.T) {
	ctx, _ := staffContext(t)
	if err := admin.NewStore(pool, fakeRefunder{}, nil, nil).
		SetProductStatus(ctx, anyProductSlug(t), "draft"); err != nil {
		t.Fatalf("seed a row: %v", err)
	}

	tests := []struct {
		name string
		stmt string
	}{
		{"update", `UPDATE audit_events SET action = 'rewritten'`},
		{"delete", `DELETE FROM audit_events`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tt.stmt); err == nil {
				t.Fatalf("%s succeeded against an append-only table", tt.name)
			} else if name := constraintFrom(err); name != "audit_events_append_only" {
				t.Errorf("refused by %q, want audit_events_append_only: %v", name, err)
			}
		})
	}

	if err := asAdmin(ctx, t, `INSERT INTO audit_events (action, entity_table)
		VALUES ('forged', 'x')`); err == nil {
		t.Error("admin inserted an audit row directly, bypassing record_audit_event")
	}

	if err := asAdmin(ctx, t, `SELECT record_audit_event(
		(SELECT id FROM users LIMIT 1), 'test.control', 'products', NULL)`); err != nil {
		t.Errorf("admin cannot call record_audit_event: %v", err)
	}
}

func constraintFrom(err error) string {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return ""
	}
	return pgErr.ConstraintName
}

func anyVariantSKU(t *testing.T) string {
	t.Helper()
	var sku string
	if err := pool.QueryRow(t.Context(),
		`SELECT v.sku FROM product_variants v
		   WHERE NOT EXISTS (SELECT 1 FROM sale_campaign_products cp WHERE cp.product_id = v.product_id)
		   ORDER BY v.sku LIMIT 1`).Scan(&sku); err != nil {
		t.Fatalf("find variant: %v", err)
	}
	return sku
}

func anyProductSlug(t *testing.T) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(),
		`SELECT slug FROM products LIMIT 1`).Scan(&slug); err != nil {
		t.Fatalf("find product: %v", err)
	}
	return slug
}

// asAdmin runs one statement with the back office's own database role: the suite
// otherwise connects as the owner, who is subject to no REVOKE at all.
func asAdmin(ctx context.Context, t *testing.T, stmt string) error {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatalf("set role admin: %v", err)
	}
	// RESET before release, or the pooled connection hands the admin role to whatever runs next.
	defer func() {
		if _, resetErr := conn.Exec(ctx, `RESET ROLE`); resetErr != nil {
			t.Errorf("reset role: %v", resetErr)
		}
	}()

	_, execErr := conn.Exec(ctx, stmt)
	return execErr
}

// TestAnAcknowledgedPaymentLeavesTheAlarm is the other half of flagging one.
// The refund is at Stripe and nothing here can see it land, so the alarm has an
// off switch or /admin/health is unhealthy forever after the first arrival —
// and an alarm that is always on is one nobody reads.
func TestAnAcknowledgedPaymentLeavesTheAlarm(t *testing.T) {
	ctx, _ := staffContext(t)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))
	baseline, healthErr := health.NewStore(pool).WorkerHealth(ctx, worker)
	if healthErr != nil {
		t.Fatalf("health before fixture: %v", healthErr)
	}

	number := placeUnpaidOrder(t)
	var orderID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE order_number = $1`, number).
		Scan(&orderID); err != nil {
		t.Fatalf("read order: %v", err)
	}
	sessionID := "cs_ack_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents)
		VALUES ($1, $2, 'requires_payment', 500000)`, orderID, sessionID); err != nil {
		t.Fatalf("open payment: %v", err)
	}

	eventID := "evt_ack_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events
			(provider, event_id, type, object_ref, payload, unreconciled)
		VALUES ('stripe', $1, 'checkout.session.completed', $2, '{}'::jsonb,
		        'refused_capture: operator must refund or post the verified money')`,
		eventID, sessionID); err != nil {
		t.Fatalf("flag the event: %v", err)
	}

	flagged, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if flagged.UnreconciledPayments != baseline.UnreconciledPayments+1 {
		t.Fatalf("flagging this event changed the alarm count from %d to %d, want one more",
			baseline.UnreconciledPayments, flagged.UnreconciledPayments)
	}
	var eventFlagged bool
	for _, event := range flagged.UnreconciledEvents {
		if event.EventID == eventID {
			eventFlagged = true
			break
		}
	}
	if !eventFlagged {
		t.Fatalf("payment alarm does not name flagged event %q", eventID)
	}

	// Merely naming the event would cancel the linked attempt and allow another
	// checkout without saying what happened to the provider money, so the HTTP
	// boundary must refuse a form that omits the safe-release conclusion.
	form := url.Values{"event": {eventID}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/health/reconcile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	healthHandler(pool).Reconcile(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/health?notflagged=1" {
		t.Fatalf("event-only HTTP resolution = %d %q, want refusal redirect",
			res.Code, res.Header().Get("Location"))
	}
	var stillOpen, stillFlagged bool
	if err := pool.QueryRow(ctx, `
		SELECT p.status = 'requires_payment', e.reconciled_at IS NULL
		FROM payments p JOIN payment_webhook_events e
		  ON e.provider = p.provider AND e.object_ref = p.provider_ref
		WHERE e.event_id = $1`, eventID).Scan(&stillOpen, &stillFlagged); err != nil {
		t.Fatalf("read refused event-only resolution: %v", err)
	}
	if !stillOpen || !stillFlagged {
		t.Fatalf("event-only resolution changed payment/event = open %v, flagged %v",
			stillOpen, stillFlagged)
	}

	beforeAudit := auditRows(t, audit.ActionReconcilePayment)
	if err := health.NewStore(pool).ReleasePaymentEventAfterRefundOrAccounting(ctx, eventID); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var paymentStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM payments WHERE provider_ref = $1`, sessionID).
		Scan(&paymentStatus); err != nil {
		t.Fatalf("read reconciled payment: %v", err)
	}
	if paymentStatus != "cancelled" {
		t.Errorf("linked payment status = %q, want cancelled so a completed session cannot be resumed",
			paymentStatus)
	}

	settled, settledErr := health.NewStore(pool).WorkerHealth(ctx, worker)
	if settledErr != nil {
		t.Fatalf("health: %v", settledErr)
	}
	if settled.UnreconciledPayments != baseline.UnreconciledPayments {
		t.Errorf("reconciling this event left the alarm count at %d, want baseline %d",
			settled.UnreconciledPayments, baseline.UnreconciledPayments)
	}
	for _, event := range settled.UnreconciledEvents {
		if event.EventID == eventID {
			t.Errorf("acknowledged event %q remains on the payment alarm", eventID)
		}
	}
	if afterAudit := auditRows(t, audit.ActionReconcilePayment); afterAudit != beforeAudit+1 {
		t.Errorf("saying the money went back by hand added %d audit rows, want 1",
			afterAudit-beforeAudit)
	}

	// A second press changes nothing: the row count is where the question is
	// asked, so there is no read-then-write for two staff members to both pass.
	if err := health.NewStore(pool).ReleasePaymentEventAfterRefundOrAccounting(
		ctx, eventID,
	); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("acknowledging it twice = %v, want ErrNotFound", err)
	}
	// And an event nobody flagged is not acknowledgeable at all.
	unflagged := "evt_plain_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events (provider, event_id, type, payload)
		VALUES ('stripe', $1, 'payment_intent.processing', '{}'::jsonb)`, unflagged); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := health.NewStore(pool).ReleasePaymentEventAfterRefundOrAccounting(
		ctx, unflagged,
	); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("acknowledging an event that was never flagged = %v, want ErrNotFound", err)
	}
}

// TestACompletePaymentWithoutAFlaggedEventHasAResolutionDoor covers provider
// completion before a capture webhook and the understood complete/unpaid event:
// neither has an unreconciled event row, so the payment state itself must make
// health unhealthy and give staff an audited, typed way to resolve it.
func TestACompletePaymentWithoutAFlaggedEventHasAResolutionDoor(t *testing.T) {
	ctx, _ := staffContext(t)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))

	number := placeUnpaidOrder(t)
	var orderID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE order_number = $1`, number).
		Scan(&orderID); err != nil {
		t.Fatalf("read order: %v", err)
	}
	providerRef := "cs_complete_health_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents)
		VALUES ($1, $2, 'requires_reconciliation', 500000)`, orderID, providerRef); err != nil {
		t.Fatalf("record complete payment: %v", err)
	}
	// This is the complete+unpaid shape: the webhook was understood and clean,
	// so it cannot be resolved through release_payment_event.
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events
			(provider, event_id, type, object_ref, payload, processed_at)
		VALUES ('stripe', $1, 'checkout.session.completed', $2, '{}'::jsonb, now())`,
		"evt_complete_unpaid_"+uuid.NewString()[:12], providerRef); err != nil {
		t.Fatalf("record understood complete event: %v", err)
	}

	flagged, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("health before resolution: %v", err)
	}
	if flagged.PaymentsReconciled() {
		t.Fatal("requires_reconciliation payment with no flagged event reads healthy")
	}
	found := false
	for _, issue := range flagged.UnreconciledCompletePayments {
		if issue.ProviderRef == providerRef {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("health did not name complete provider reference %q", providerRef)
	}

	beforeAudit := auditRows(t, audit.ActionReconcilePayment)
	if reconcileErr := health.NewStore(pool).ReconcileCompletePayment(ctx, providerRef,
		health.CompletePaymentUnpaidOrRefunded); reconcileErr != nil {
		t.Fatalf("reconcile complete payment: %v", reconcileErr)
	}
	var status string
	if queryErr := pool.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, providerRef).Scan(&status); queryErr != nil {
		t.Fatalf("read resolved payment: %v", queryErr)
	}
	if status != "reconciled" {
		t.Errorf("resolved complete payment status = %q, want reconciled", status)
	}

	settled, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("health after resolution: %v", err)
	}
	for _, issue := range settled.UnreconciledCompletePayments {
		if issue.ProviderRef == providerRef {
			t.Errorf("resolved provider reference %q remains on health", providerRef)
		}
	}
	if got := auditRows(t, audit.ActionReconcilePayment); got != beforeAudit+1 {
		t.Errorf("reconciling complete payment added %d audit rows, want 1", got-beforeAudit)
	}
	if err := health.NewStore(pool).ReconcileCompletePayment(ctx, providerRef,
		health.CompletePaymentUnpaidOrRefunded); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("reconciling complete payment twice = %v, want ErrNotFound", err)
	}
}

// TestReleasedStockPaidAttributionReturnsARefundInstruction proves the admin
// boundary does not turn the capture fence into a generic 500. Health removes
// the impossible paid action, and a stale form submitted across a concurrent
// sweep returns an actionable refund notice while leaving reconciliation open.
func TestReleasedStockPaidAttributionReturnsARefundInstruction(t *testing.T) {
	ctx, _ := staffContext(t)
	number, orderID, _ := pendingOrderHoldingStock(t)
	providerRef := "cs_admin_released_stock_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx,
		`SELECT open_payment($1, $2, 100000)`, orderID, providerRef); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	var reservationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		UPDATE inventory_reservations
		SET created_at = now() - interval '2 hours',
		    expires_at = now() - interval '1 hour'
		WHERE order_id = $1 AND state = 'held'
		RETURNING id`, orderID).Scan(&reservationID); err != nil {
		t.Fatalf("expire hold: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT release_reservation($1)`, reservationID); err != nil {
		t.Fatalf("release hold before recovery: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT record_complete_payment($1, $2, 100000)`, orderID, providerRef); err != nil {
		t.Fatalf("record delayed complete session: %v", err)
	}

	view, err := health.NewStore(pool).WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	refundOnly := false
	for _, issue := range view.UnreconciledCompletePayments {
		if issue.ProviderRef == providerRef && issue.OrderNumber == number &&
			!issue.PaidAttributionAllowed {
			refundOnly = true
		}
	}
	if !refundOnly {
		t.Fatal("released-stock complete payment still offered paid attribution")
	}

	form := url.Values{
		"payment":    {providerRef},
		"resolution": {"paid"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/health/reconcile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	healthHandler(pool).Reconcile(res, req)
	if res.Code != http.StatusSeeOther ||
		res.Header().Get("Location") != "/admin/health?mustrefund=1" {
		t.Fatalf("stale paid form = %d %q, want actionable refund redirect",
			res.Code, res.Header().Get("Location"))
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, providerRef).Scan(&status); err != nil {
		t.Fatalf("read refused payment: %v", err)
	}
	if status != "requires_reconciliation" {
		t.Fatalf("refused paid form changed payment to %q, want requires_reconciliation", status)
	}
}

func TestCancellingAnOrderInTheBackOfficeReturnsItsStock(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(restockAdminPool(t, pool), fakeRefunder{}, nil, nil)

	var vid uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.stock_quantity > pv.safety_stock + 1
		ORDER BY pv.id LIMIT 1`).Scan(&vid); err != nil {
		t.Fatalf("find a sellable variant: %v", err)
	}
	number := placeHeldOrder(t, vid)

	var held int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&held); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	subscriptions := waitForRestock(t, vid)
	var staff uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '取消人員')
		RETURNING id`, "cancel-"+number+"@goen.invalid").Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	// record_audit_event reads the actor from the CONTEXT, not from the parameter.
	staffCtx := account.WithUser(ctx, account.User{ID: staff.String(), Role: "admin"})
	if _, err := s.Advance(staffCtx, number, "cancelled", uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	assertRestockQueued(t, pool, subscriptions)
	var after int32
	var state string
	if err := pool.QueryRow(ctx, `
		SELECT pv.stock_quantity,
		       (SELECT r.state FROM inventory_reservations r
		        JOIN orders o ON o.id = r.order_id
		        WHERE o.order_number = $2 LIMIT 1)
		FROM product_variants pv WHERE pv.id = $1`, vid, number).Scan(&after, &state); err != nil {
		t.Fatalf("read stock after: %v", err)
	}
	if after != held+1 {
		t.Errorf("stock is %d after cancelling, want %d — the hold was not released", after, held+1)
	}
	if state != "released" {
		t.Errorf("the hold is %s, want released", state)
	}
}

// TestBackOfficeCancellationReadsHoldsAfterWinningTheOrderLock is the admin
// counterpart of the customer cancellation race. The expiry release queues
// first, changes the reservation while retaining the order lock, then commits;
// Advance must take its held snapshot only after that commit and still finish.
func TestBackOfficeCancellationReadsHoldsAfterWinningTheOrderLock(t *testing.T) {
	ctx := t.Context()
	var variantID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.stock_quantity > pv.safety_stock + 1
		ORDER BY pv.id LIMIT 1`).Scan(&variantID); err != nil {
		t.Fatalf("find a sellable variant: %v", err)
	}
	number := placeHeldOrder(t, variantID)
	var orderID, reservationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT o.id, r.id FROM orders o
		JOIN inventory_reservations r ON r.order_id = o.id
		WHERE o.order_number = $1 AND r.state = 'held'`, number).
		Scan(&orderID, &reservationID); err != nil {
		t.Fatalf("read order and held reservation: %v", err)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin order blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	var blockerPID int32
	if lockErr := blocker.QueryRow(ctx, `
		SELECT pg_backend_pid() FROM orders WHERE id = $1 FOR UPDATE`, orderID).
		Scan(&blockerPID); lockErr != nil {
		t.Fatalf("lock order: %v", lockErr)
	}

	sweepPool := admintest.NamedPool(t, pool, "admin-sweep-before-cancel")
	sweepTx, err := sweepPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin sweep release: %v", err)
	}
	defer func() { _ = sweepTx.Rollback(context.WithoutCancel(ctx)) }()
	sweepDone := make(chan error, 1)
	go func() {
		_, releaseErr := sweepTx.Exec(ctx, `SELECT release_reservation($1)`, reservationID)
		sweepDone <- releaseErr
	}()
	sweepPID := admintest.WaitForBlockedApplication(t, pool, ctx, "admin-sweep-before-cancel", blockerPID)

	var staff uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '併發取消人員')
		RETURNING id`, "cancel-race-"+uuid.NewString()+"@goen.invalid").Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	staffCtx := account.WithUser(ctx, account.User{ID: staff.String(), Role: "admin"})
	cancelPool := admintest.NamedPool(t, pool, "admin-cancel-behind-sweep")
	cancelDone := make(chan error, 1)
	go func() {
		_, advanceErr := admin.NewStore(cancelPool, fakeRefunder{}, nil, nil).Advance(
			staffCtx, number, "cancelled", uuid.NullUUID{UUID: staff, Valid: true})
		cancelDone <- advanceErr
	}()
	// PostgreSQL reports the earlier queued waiter as a soft blocker. Waiting
	// behind that PID also pins which operation will win when blocker commits.
	admintest.WaitForBlockedApplication(t, pool, ctx, "admin-cancel-behind-sweep", sweepPID)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release order blocker: %v", err)
	}
	if err := <-sweepDone; err != nil {
		t.Fatalf("sweeper release after order unlock: %v", err)
	}
	select {
	case err := <-cancelDone:
		t.Fatalf("admin cancellation passed an uncommitted sweep release: %v", err)
	default:
	}
	if err := sweepTx.Commit(ctx); err != nil {
		t.Fatalf("commit sweep release: %v", err)
	}
	if err := <-cancelDone; err != nil {
		t.Fatalf("admin cancel after sweep won the lock: %v", err)
	}

	var status, state string
	if err := pool.QueryRow(ctx, `
		SELECT o.fulfillment_status, r.state
		FROM orders o JOIN inventory_reservations r ON r.order_id = o.id
		WHERE o.id = $1`, orderID).Scan(&status, &state); err != nil {
		t.Fatalf("read final state: %v", err)
	}
	if status != "cancelled" || state != "released" {
		t.Errorf("final state is order=%s reservation=%s, want cancelled/released", status, state)
	}
}

func placeHeldOrder(t *testing.T, vid uuid.UUID) string {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		SELECT next_order_number(), v.id, sm.code, v.name
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, 500000, 1 FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $2`, orderID, vid); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'held@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		orderID, vid, "admin-cancel:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

func TestShippingEnqueuesTheDispatchNotice(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number := shippableOrder(t, "en")

	var staff uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '出貨')
		RETURNING id`, "dispatch-"+number+"@goen.invalid").Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	staffCtx := account.WithUser(ctx, account.User{ID: staff.String(), Role: "admin"})

	if err := s.Ship(staffCtx, number, admin.Dispatch{Carrier: "black_cat", Tracking: "903-2214-0001"},
		uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("ship: %v", err)
	}

	// Found by what the notice IS about, not by the dedupe key: a test bound to
	// the key asserts the deduplication scheme rather than the notice.
	var payload []byte
	if err := pool.QueryRow(ctx,
		`SELECT payload FROM outbox_messages
		 WHERE topic = 'order.shipped' AND payload->>'tracking' = $1`,
		"903-2214-0001").Scan(&payload); err != nil {
		t.Fatalf("no dispatch notice was enqueued for %s: %v", number, err)
	}
	var got email.OrderShipped
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode notice: %v", err)
	}
	want := email.OrderShipped{
		OrderNumber: number, Email: "ship@example.com", Name: "收件人",
		Carrier: "black_cat", Tracking: "903-2214-0001",
		Locale: "en",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("dispatch notice (-want +got):\n%s", diff)
	}
}

func shippableOrder(t *testing.T, locale string) string {
	t.Helper()
	return shippableOrderFor(t, locale, false)
}

// shippableOrderFor is shippableOrder for a home delivery, or, when pickup, for
// a store pickup chosen before the order settles, as checkout places one.
func shippableOrderFor(t *testing.T, locale string, pickup bool) string {
	t.Helper()
	ctx := t.Context()

	var variantID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.is_active AND p.status = 'active' AND pv.stock_quantity - pv.safety_stock > 2
		LIMIT 1`).Scan(&variantID); err != nil {
		t.Fatalf("find variant: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents, locale)
		SELECT next_order_number(), v.id, sm.code, v.name, 0, $1
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE NOT $2 OR sm.destination_kind = 'pickup_point'
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, locale, pickup).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, 100000, 1 FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $2`, orderID, variantID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		orderID, variantID, "ship-fixture:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	private := `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'ship@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`
	if pickup {
		private = `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                pickup_chain, pickup_store_code, pickup_store_name)
		VALUES ($1, 'ship@example.com', '收件人', '0912345678',
		        'family_mart', '012345', '台北車站門市')`
	}
	if _, err := tx.Exec(ctx, private, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`,
		orderID, "cs_ship_"+number); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`,
		"cs_ship_"+number); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move to picking: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

// TestAnOverClaimIsRefusedInWordsRatherThanByAConstraint goes through a goodwill refund:
// a second return meets return_within_shipment first and never reaches this guard.
func TestAnOverClaimIsRefusedInWordsRatherThanByAConstraint(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	requestID, orderNumber := returnedOrder(t, 2)

	var paymentID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT p.id FROM payments p JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND p.status = 'succeeded'`,
		orderNumber).Scan(&paymentID); err != nil {
		t.Fatalf("find payment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds
		    (payment_id, request_key, amount_cents, reason, status, provider_ref, succeeded_at)
		VALUES ($1,$2,150000,'善意退款','succeeded',$3,now())`,
		paymentID, "goodwill:"+orderNumber, "re_goodwill_"+requestID.String()); err != nil {
		t.Fatalf("post the goodwill refund: %v", err)
	}

	err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{})
	if err == nil {
		t.Fatal("a return claiming more than remains was approved")
	}
	if !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("refused with %v, want ErrRefused", err)
	}
	// Figures only: matching the words would bind this to a message that is translated.
	for _, want := range []string{"200000", "150000", "50000"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "refunds_within_capture") {
		t.Errorf("the constraint reached the staff member: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read the return: %v", err)
	}
	if status != "requested" {
		t.Errorf("a refused approval left the return %s", status)
	}
}

func TestTheReturnQueueShowsWhatIsComingBack(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	requestID, _ := returnedOrder(t, 2)

	view, err := s.Returns(ctx)
	if err != nil {
		t.Fatalf("read the queue: %v", err)
	}

	var found *adminpages.Return
	for i := range view.Rows {
		if view.Rows[i].ID == requestID.String() {
			found = &view.Rows[i]
		}
	}
	if found == nil {
		t.Fatalf("the return %s is not in the queue", requestID)
	}
	if len(found.Lines) == 0 {
		t.Fatal("the queue row carries no lines — the decision is still blind")
	}
	line := found.Lines[0]
	if line.SKU == "" || line.Name == "" || line.Quantity != 2 {
		t.Errorf("the line is %+v, want a sku, a name and 2 units", line)
	}
}

func TestTheReturnQueueNamesTheRefundChannels(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	tests := []struct {
		name   string
		setup  func(t *testing.T) uuid.UUID
		card   int64
		credit int64
		want   string
		not    string
	}{
		{
			name: "credit-only",
			setup: func(t *testing.T) uuid.UUID {
				t.Helper()
				id, _, _ := creditFundedReturn(t, 2, 200000)
				return id
			},
			credit: 200000,
			want:   "店儲 NT$2,000 退回額度",
			not:    "走 Stripe",
		},
		{
			name: "card and credit split",
			setup: func(t *testing.T) uuid.UUID {
				t.Helper()
				id, _, _ := creditFundedReturn(t, 2, 60000)
				return id
			},
			card:   140000,
			credit: 60000,
			want:   "卡款 NT$1,400 走 Stripe，店儲 NT$600 退回額度",
		},
		{
			name: "card-only",
			setup: func(t *testing.T) uuid.UUID {
				t.Helper()
				id, _ := returnedOrder(t, 2)
				return id
			},
			card: 200000,
			want: "卡款 NT$2,000 走 Stripe",
			not:  "額度",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestID := tt.setup(t)
			if err := s.Decide(ctx, requestID.String(), "approved", "核准", "", uuid.NullUUID{}); err != nil {
				t.Fatalf("Decide: %v", err)
			}
			view, err := s.Returns(ctx)
			if err != nil {
				t.Fatalf("Returns: %v", err)
			}
			var found *adminpages.Return
			for i := range view.Rows {
				if view.Rows[i].ID == requestID.String() {
					found = &view.Rows[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("return %s is not in the queue", requestID)
			}
			if found.CardRefundCents != tt.card || found.CreditRefundCents != tt.credit {
				t.Errorf("frozen split card/credit = %d/%d, want %d/%d",
					found.CardRefundCents, found.CreditRefundCents, tt.card, tt.credit)
			}
			zh := i18n.WithLocale(ctx, i18n.ZhHant)
			got := found.PayoutChannel(zh)
			if got != tt.want {
				t.Errorf("PayoutChannel(zh-Hant) = %q, want %q", got, tt.want)
			}
			if tt.not != "" && strings.Contains(got, tt.not) {
				t.Errorf("PayoutChannel(zh-Hant) = %q, must not mention %q", got, tt.not)
			}
			en := found.PayoutChannel(i18n.WithLocale(ctx, i18n.En))
			if !strings.Contains(strings.ToLower(en), "stripe") && tt.card > 0 {
				t.Errorf("PayoutChannel(en) = %q, want the card half named", en)
			}
			if !strings.Contains(strings.ToLower(en), "store credit") && tt.credit > 0 {
				t.Errorf("PayoutChannel(en) = %q, want the credit half named", en)
			}
		})
	}
}

func TestTheReturnQueueUsesAConstantQueryCountForAnyNumberOfApprovedRows(t *testing.T) {
	ctx := t.Context()
	isolated := dbtest.Pool(t)
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatalf("read isolated return queue seed: %v", err)
	}
	if _, execErr := isolated.Exec(ctx, string(seed)); execErr != nil {
		t.Fatalf("load isolated return queue seed: %v", execErr)
	}
	first, _ := returnedOrderOn(t, isolated, 1)
	second, _ := returnedOrderOn(t, isolated, 2)

	var queries atomic.Int64
	var queryNames []string
	var queryNamesMu sync.Mutex
	config := isolated.Config()
	config.ConnConfig.Tracer = returnQueryTracer{
		queries: &queries,
		mu:      &queryNamesMu,
		names:   &queryNames,
	}
	traced, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open traced admin pool: %v", err)
	}
	t.Cleanup(traced.Close)

	if _, execErr := traced.Exec(ctx, `
		UPDATE return_requests
		SET status = 'approved', decided_at = now(), created_at = now() + interval '100 years'
		WHERE id = ANY($1::uuid[])`, []uuid.UUID{first, second}); execErr != nil {
		t.Fatalf("approve the return queue fixtures: %v", execErr)
	}

	counted := context.WithValue(ctx, returnQueryCountKey{}, struct{}{})
	view, err := admin.NewStore(traced, fakeRefunder{}, nil, nil).Returns(counted)
	if err != nil {
		t.Fatalf("read the traced return queue: %v", err)
	}
	found := map[string]bool{first.String(): false, second.String(): false}
	for i := range view.Rows {
		if _, ok := found[view.Rows[i].ID]; ok {
			found[view.Rows[i].ID] = true
		}
	}
	for id, ok := range found {
		if !ok {
			t.Errorf("approved return %s is absent from the traced queue", id)
		}
	}
	got := queries.Load()
	if got != 4 {
		t.Errorf("Returns() made %d queries, want 4 (queue, lines, payout facts, assessments)", got)
	}
	queryNamesMu.Lock()
	gotNames := slices.Clone(queryNames)
	queryNamesMu.Unlock()
	wantNames := []string{
		"-- name: ReturnQueue :many",
		"-- name: ReturnLines :many",
		"-- name: ReturnPayoutFacts :many",
		"-- name: LatestEligibilityAssessments :many",
	}
	if diff := cmp.Diff(wantNames, gotNames); diff != "" {
		t.Errorf("Returns() query names mismatch (-want +got):\n%s", diff)
	}
	t.Logf("Returns() query count = %d (queue, lines, payout facts, assessments)", got)
}

func TestTheRescissionWindowIsCountedOnTheShopsCalendar(t *testing.T) {
	isolated := admintest.Pool(t)
	ctx, _ := staffContextOn(t, isolated)
	s := admin.NewStore(isolated, fakeRefunder{}, nil, nil)

	cases := []struct {
		name           string
		delivered      string
		requested      string
		wantWindow     string
		wantRescission bool
	}{
		{
			name:           "a parcel handed over in the Taipei morning",
			delivered:      "2026-08-25T07:00:00+08:00",
			requested:      "2026-09-01T12:00:00+08:00",
			wantWindow:     "within",
			wantRescission: true,
		},
		{
			name:           "a request made in the Taipei small hours one day late",
			delivered:      "2026-08-25T12:00:00+08:00",
			requested:      "2026-09-02T06:00:00+08:00",
			wantWindow:     "goodwill",
			wantRescission: false,
		},
		{
			name:           "the last shop day of the advertised fourteen",
			delivered:      "2026-08-25T07:00:00+08:00",
			requested:      "2026-09-08T12:00:00+08:00",
			wantWindow:     "goodwill",
			wantRescission: false,
		},
		{
			name:           "a request in the Taipei small hours one day past fourteen",
			delivered:      "2026-08-25T12:00:00+08:00",
			requested:      "2026-09-09T06:00:00+08:00",
			wantWindow:     "after",
			wantRescission: false,
		},
		{
			name:           "a January filing still statutory when read in September",
			delivered:      "2026-01-01T07:00:00+08:00",
			requested:      "2026-01-06T12:00:00+08:00",
			wantWindow:     "within",
			wantRescission: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// These hours are the lock: 07:00 Taipei is 23:00 UTC on the
			// previous day, and 06:00 Taipei is 22:00 UTC on the previous
			// day. Moving either into the middle of the day makes shop_day(x)
			// equal x::date and silently makes this test agree with the defect.
			delivered, err := time.Parse(time.RFC3339, tc.delivered)
			if err != nil {
				t.Fatalf("parse delivered_at: %v", err)
			}
			requested, err := time.Parse(time.RFC3339, tc.requested)
			if err != nil {
				t.Fatalf("parse requested_at: %v", err)
			}
			requestID := returnedOrderAtOn(t, isolated, delivered, requested)

			view, err := s.Returns(ctx)
			if err != nil {
				t.Fatalf("read the queue: %v", err)
			}
			var found *adminpages.Return
			for i := range view.Rows {
				if view.Rows[i].ID == requestID.String() {
					found = &view.Rows[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("the return %s is not in the queue", requestID)
			}
			if found.Window != tc.wantWindow {
				t.Errorf("window is %q, want %q", found.Window, tc.wantWindow)
			}
			if got := found.Rescission(); got != tc.wantRescission {
				t.Errorf("Rescission() is %t, want %t", got, tc.wantRescission)
			}
		})
	}
}

func TestOneUploadCanBeAttachedToTwoProducts(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	const digest = "aa11bb22cc33dd44ee55ff6677889900aa11bb22cc33dd44ee55ff6677889900"
	if _, err := pool.Exec(ctx, `
		-- byte_size must equal length(bytes): media_objects_size_matches refuses
		-- a row that claims a size its own bytes do not have.
		INSERT INTO media_objects (digest, content_type, byte_size, width, height, bytes)
		VALUES ($1, 'image/png', 1, 800, 600, '\x00'::bytea)
		ON CONFLICT (digest) DO NOTHING`, digest); err != nil {
		t.Fatalf("store the image: %v", err)
	}

	first, second := twoProducts(t)
	if err := s.AttachImage(ctx, first, digest, "第一個商品", "First product", "", 800, 600); err != nil {
		t.Fatalf("attach to the first: %v", err)
	}
	if err := s.AttachImage(ctx, second, digest, "第二個商品", "", "", 800, 600); err != nil {
		t.Fatalf("attach the SAME image to the second: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM product_images WHERE storage_key = $1`, digest).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("the image is attached to %d products, want 2", n)
	}
	if err := s.AttachImage(ctx, first, digest, "再一次", "", "", 800, 600); err == nil {
		t.Error("the same image was attached to one product twice")
	}
}

func twoProducts(t *testing.T) (first, second string) {
	t.Helper()
	slugs := make([]string, 0, 2)
	for i := range 2 {
		slug := "picker-" + uuid.NewString()[:8] + "-" + strconv.Itoa(i)
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO products (brand_id, category_id, slug, name)
			SELECT b.id, c.id, $1, $1 FROM brands b, categories c
			ORDER BY b.id, c.id LIMIT 1`, slug); err != nil {
			t.Fatalf("create product: %v", err)
		}
		slugs = append(slugs, slug)
	}
	return slugs[0], slugs[1]
}

func TestADeliveryAddressCanBeCorrectedUntilItShips(t *testing.T) {
	ctx, staffID := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number := shippableOrder(t, "zh-Hant")

	correction := &admin.Delivery{
		Email: "fixed@example.com", Recipient: "收件人", Phone: "0922333444",
		PostalCode: "106", City: "台北市", District: "大安區", Street: "正確的地址 99 號",
	}
	if err := s.CorrectDelivery(ctx, number, correction); err != nil {
		t.Fatalf("correct a picking order: %v", err)
	}
	if got := streetOf(t, number); got != "正確的地址 99 號" {
		t.Errorf("the address is %q after the correction", got)
	}

	if err := s.Ship(ctx, number, admin.Dispatch{Carrier: "black_cat", Tracking: "903-2214-9999"},
		uuid.NullUUID{UUID: staffID, Valid: true}); err != nil {
		t.Fatalf("ship: %v", err)
	}
	correction.Street = "出貨後偷改的地址"
	if err := s.CorrectDelivery(ctx, number, correction); !errors.Is(err, admin.ErrTooLateToCorrect) {
		t.Fatalf("a shipped order was corrected: %v", err)
	}
	if got := streetOf(t, number); got != "正確的地址 99 號" {
		t.Errorf("the address changed after dispatch: %q", got)
	}
}

func TestCorrectingADeliveryDoesNotWriteTheAddressIntoTheAuditTrail(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number := shippableOrder(t, "zh-Hant")

	const street = "非常獨特的街道名稱 12345"
	if err := s.CorrectDelivery(ctx, number, &admin.Delivery{
		Email: "audit@example.com", Recipient: "收件人", Phone: "0922333444",
		PostalCode: "106", City: "台北市", District: "大安區", Street: street,
	}); err != nil {
		t.Fatalf("correct: %v", err)
	}

	var after string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(after::text, '') FROM audit_events
		WHERE action = 'order.delivery' ORDER BY occurred_at DESC LIMIT 1`).Scan(&after); err != nil {
		t.Fatalf("read the audit row: %v", err)
	}
	if after == "" {
		t.Fatal("no audit row was written for the correction")
	}
	if strings.Contains(after, street) {
		t.Errorf("the customer's address is in the audit trail: %s", after)
	}
	if !strings.Contains(after, number) {
		t.Errorf("the audit row does not say which order changed: %s", after)
	}
}

func TestCorrectingAPickupOrderCannotTurnItIntoAnAddressOne(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number := pickupOrderForCorrection(t)

	if err := s.CorrectDelivery(ctx, number, &admin.Delivery{
		Email: "pickup@example.com", Recipient: "收件人", Phone: "0922333444",
		PostalCode: "106", City: "台北市", District: "大安區", Street: "不該存下來的地址",
		PickupChain: "hi_life", PickupStoreCode: "778899", PickupStoreName: "民生門市",
	}); err != nil {
		t.Fatalf("correct a pickup order: %v", err)
	}

	var street, brand, code *string
	if err := pool.QueryRow(ctx, `
		SELECT pd.street, pd.pickup_chain, pd.pickup_store_code
		FROM order_private_data pd JOIN orders o ON o.id = pd.order_id
		WHERE o.order_number = $1`, number).Scan(&street, &brand, &code); err != nil {
		t.Fatalf("read the delivery: %v", err)
	}
	if street != nil {
		t.Errorf("a pickup order kept a street address: %q", *street)
	}
	if brand == nil || *brand != "hi_life" || code == nil || *code != "778899" {
		t.Errorf("the pickup point was not updated: %v/%v", brand, code)
	}
}

func streetOf(t *testing.T, number string) string {
	t.Helper()
	var street string
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce(pd.street, '') FROM order_private_data pd
		JOIN orders o ON o.id = pd.order_id WHERE o.order_number = $1`,
		number).Scan(&street); err != nil {
		t.Fatalf("read street: %v", err)
	}
	return street
}

func pickupOrderForCorrection(t *testing.T) string {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.destination_kind = 'pickup_point'
		ORDER BY v.effective_at DESC LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create pickup order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'FIX-SKU', '測試商品', 100000, 1)`, orderID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                pickup_chain, pickup_store_code, pickup_store_name)
		VALUES ($1, 'pickup@example.com', '收件', '0912345678',
		        'family_mart', '012345', '台北車站門市')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

func TestAReturnPaysBackBothSources(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	requestID, orderNumber, accountID := creditFundedReturn(t, 2, 60000)

	if err := s.Decide(ctx, requestID.String(), "approved", "退貨完成", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	var refunded int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(r.amount_cents), 0) FROM refunds r
		JOIN payments p ON p.id = r.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND r.status = 'succeeded'`,
		orderNumber).Scan(&refunded); err != nil {
		t.Fatalf("read refunds: %v", err)
	}
	if refunded != 140000 {
		t.Errorf("card refunded %d, want 140000 — the whole capture, card first", refunded)
	}

	var compensated int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(e.amount_cents), 0) FROM store_credit_entries e
		JOIN orders o ON o.id = e.order_id
		WHERE o.order_number = $1 AND e.amount_cents > 0 AND e.reverses_id IS NULL`,
		orderNumber).Scan(&compensated); err != nil {
		t.Fatalf("read compensation: %v", err)
	}
	if compensated != 60000 {
		t.Errorf("credit compensated %d, want 60000", compensated)
	}
	if got := creditBalanceOf(t, accountID); got != 60000 {
		t.Errorf("balance = %d, want 60000 — the credit they spent came back", got)
	}
}

func TestAPartialReturnPaysTheCardFirst(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	requestID, orderNumber, accountID := creditFundedReturn(t, 1, 60000)

	if err := s.Decide(ctx, requestID.String(), "approved", "退一件", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	var refunded int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(r.amount_cents), 0) FROM refunds r
		JOIN payments p ON p.id = r.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND r.status = 'succeeded'`,
		orderNumber).Scan(&refunded); err != nil {
		t.Fatalf("read refunds: %v", err)
	}
	if refunded != 100000 {
		t.Errorf("card refunded %d, want the whole claim of 100000 — card first", refunded)
	}
	if got := creditBalanceOf(t, accountID); got != 0 {
		t.Errorf("balance = %d, want 0 — the card paid the whole claim, so none of "+
			"the credit was needed", got)
	}
}

func TestAWhollyCreditFundedReturnNeedsNoProvider(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	requestID, orderNumber, accountID := creditFundedReturn(t, 2, 200000)

	if decideErr := s.Decide(ctx, requestID.String(), "approved", "全額購物金", "", uuid.NullUUID{}); decideErr != nil {
		t.Fatalf("Decide: %v", decideErr)
	}

	var refunds int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM refunds r
		JOIN payments p ON p.id = r.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1`, orderNumber).Scan(&refunds); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	if refunds != 0 {
		t.Errorf("%d refund rows for an order with no card payment, want 0", refunds)
	}
	if got := creditBalanceOf(t, accountID); got != 200000 {
		t.Errorf("balance = %d, want 200000 — every cent came back as credit", got)
	}
}

func TestACreditOnlyRefundIsOnTheCustomersTimeline(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	requestID, _, _ := creditFundedReturn(t, 2, 200000)

	if err := s.Decide(ctx, requestID.String(), "approved", "全額購物金", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	var events int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events e
		WHERE e.return_request_id = $1 AND e.kind = 'refunded'`, requestID).Scan(&events); err != nil {
		t.Fatalf("count timeline events: %v", err)
	}
	if events != 1 {
		t.Errorf("%d refunded events after a credit-only refund, want 1 — the customer "+
			"has no per-entry credit history, so without this their timeline says no money moved", events)
	}

	var entries int
	var credited int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*), coalesce(sum(amount_cents), 0)::bigint
		FROM store_credit_entries
		WHERE idempotency_key = 'return-credit:' || $1::text`, requestID).
		Scan(&entries, &credited); err != nil {
		t.Fatalf("read returned credit: %v", err)
	}
	if entries != 1 || credited != 200000 {
		t.Errorf("returned credit is %d row(s) totalling %d, want one row of 200000", entries, credited)
	}
}

// TestAMissingRefundTimelineEventIsRecoveredAfterMoneyCommits injects the
// boundary where the credit ledger commits but the separately appended customer
// timeline fails. The approved return must keep offering useful work, recreate
// exactly one event without paying twice, and only then become completable.
func TestAMissingRefundTimelineEventIsRecoveredAfterMoneyCommits(t *testing.T) {
	ctx, staff := staffContext(t)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, _, accountID := creditFundedReturn(t, 2, 200000)

	const trigger = "test_fail_return_refunded_event"
	const function = "test_fail_return_refunded_event_fn"
	dropFailure := func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `
			DROP TRIGGER IF EXISTS test_fail_return_refunded_event ON order_events;
			DROP FUNCTION IF EXISTS test_fail_return_refunded_event_fn();`)
	}
	dropFailure()
	t.Cleanup(dropFailure)
	install := fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'injected return event failure';
		END;
		$$;
		CREATE TRIGGER %s
		BEFORE INSERT ON order_events
		FOR EACH ROW
		WHEN (NEW.return_request_id = '%s'::uuid)
		EXECUTE FUNCTION %s();`, function, trigger, requestID, function)
	if _, err := pool.Exec(ctx, install); err != nil {
		t.Fatalf("install event failure: %v", err)
	}

	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	if err := s.Decide(ctx, requestID.String(), "approved", "全額購物金", "", uuid.NullUUID{}); err == nil {
		t.Fatal("injected timeline failure was reported as a complete payout")
	}
	if got := creditBalanceOf(t, accountID); got != 200000 {
		t.Fatalf("credit after event failure = %d, want the committed 200000", got)
	}
	var events int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events WHERE return_request_id = $1`, requestID).
		Scan(&events); err != nil {
		t.Fatalf("count failed timeline append: %v", err)
	}
	if events != 0 {
		t.Fatalf("failed timeline append left %d events, want 0", events)
	}
	view, err := s.Returns(ctx)
	if err != nil {
		t.Fatalf("read event-recovery queue: %v", err)
	}
	found := false
	for i := range view.Rows {
		if view.Rows[i].ID != requestID.String() {
			continue
		}
		found = true
		if !view.Rows[i].PayoutOutstanding || view.Rows[i].PayoutBlocked {
			t.Fatalf("missing event renders outstanding=%v blocked=%v, want true/false",
				view.Rows[i].PayoutOutstanding, view.Rows[i].PayoutBlocked)
		}
	}
	if !found {
		t.Fatalf("return %s is absent from the event-recovery queue", requestID)
	}

	dropFailure()
	if err := s.Decide(ctx, requestID.String(), "approved", "補登退款事件", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("retry missing refunded event: %v", err)
	}
	if got := creditBalanceOf(t, accountID); got != 200000 {
		t.Errorf("credit after event retry = %d, want no duplicate posting", got)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events WHERE return_request_id = $1`, requestID).
		Scan(&events); err != nil {
		t.Fatalf("count repaired timeline append: %v", err)
	}
	if events != 1 {
		t.Errorf("event retry left %d refunded events, want exactly 1", events)
	}
	if err := s.Decide(ctx, requestID.String(), "approved", "重複補登", "", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("second event retry = %v, want an already-settled refusal", err)
	}

	if err := s.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: returnLineID(t, requestID), Received: 2, Restocked: 0,
	}}, actor); err != nil {
		t.Fatalf("inspect event-repaired return: %v", err)
	}
	if err := s.CompleteReturn(ctx, requestID.String(), "退款事件已補登", actor); err != nil {
		t.Fatalf("complete event-repaired return: %v", err)
	}
}

func TestASplitReturnStillPostsCreditWhenTheCardAttemptTerminates(t *testing.T) {
	tests := []struct {
		name     string
		refunder fakeRefunder
	}{
		{name: "provider object failed", refunder: fakeRefunder{state: admin.RefundFailed}},
		{name: "create API rejected", refunder: fakeRefunder{refundErr: fmt.Errorf(
			"%w: provider rejected create", admin.ErrRefundCreateRejected)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := staffContext(t)
			requestID, _, accountID := creditFundedReturn(t, 2, 60000)
			s := admin.NewStore(pool, tt.refunder, nil, nil)

			err := s.Decide(ctx, requestID.String(), "approved", "split terminal", "", uuid.NullUUID{})
			if !errors.Is(err, admin.ErrRefundIncomplete) || errors.Is(err, admin.ErrRefused) {
				t.Fatalf("split terminal decision = %v, want only ErrRefundIncomplete", err)
			}
			if got := creditBalanceOf(t, accountID); got != 60000 {
				t.Errorf("credit after terminal card outcome = %d, want frozen 60000", got)
			}
			var refundStatus string
			var events int
			if queryErr := pool.QueryRow(ctx, `
				SELECT status FROM refunds WHERE return_request_id = $1`, requestID).
				Scan(&refundStatus); queryErr != nil {
				t.Fatalf("read terminal card attempt: %v", queryErr)
			}
			if refundStatus != "failed" {
				t.Errorf("card attempt = %q, want failed", refundStatus)
			}
			if queryErr := pool.QueryRow(ctx, `
				SELECT count(*) FROM order_events WHERE return_request_id = $1`, requestID).
				Scan(&events); queryErr != nil {
				t.Fatalf("count split terminal timeline event: %v", queryErr)
			}
			if events != 0 {
				t.Errorf("split terminal timeline events = %d, want 0 — refunded is the "+
					"completed-tense word and the card half has not settled", events)
			}

			queue, err := s.Returns(ctx)
			if err != nil {
				t.Fatalf("read split terminal queue: %v", err)
			}
			for i := range queue.Rows {
				if queue.Rows[i].ID == requestID.String() {
					if !queue.Rows[i].CanRetryPayout() {
						t.Fatalf("split terminal row has outstanding/blocked %v/%v, want card retry",
							queue.Rows[i].PayoutOutstanding, queue.Rows[i].PayoutBlocked)
					}
					return
				}
			}
			t.Fatalf("split terminal return %s absent from recovery queue", requestID)
		})
	}
}

func TestASplitRefundWhoseCardIsPendingStillRecordsTheCreditThatLanded(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{state: admin.RefundPending}, nil, nil)
	requestID, _, _ := creditFundedReturn(t, 2, 60000)

	if err := s.Decide(ctx, requestID.String(), "approved", "分拆退款", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	var refundState string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM refunds WHERE return_request_id = $1`, requestID).
		Scan(&refundState); err != nil {
		t.Fatalf("read card refund: %v", err)
	}
	if refundState != "pending" {
		t.Fatalf("card refund is %q, want pending — the fixture does not distinguish accepted from moved", refundState)
	}

	var credited int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(amount_cents), 0)::bigint FROM store_credit_entries
		WHERE idempotency_key = 'return-credit:' || $1::text`, requestID).
		Scan(&credited); err != nil {
		t.Fatalf("read returned credit: %v", err)
	}
	if credited != 60000 {
		t.Fatalf("returned credit is %d, want 60000 — no money moved, so this proves nothing", credited)
	}

	var events int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events e
		WHERE e.return_request_id = $1 AND e.kind = 'refunded'`, requestID).Scan(&events); err != nil {
		t.Fatalf("count timeline events: %v", err)
	}
	if events != 0 {
		t.Errorf("%d refunded events after credit landed while the card stayed pending, want 0 — "+
			"refunded tells the customer the money is back", events)
	}

	if err := s.Decide(ctx, requestID.String(), "approved", "分拆退款", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("retry while the card is still pending: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events e
		WHERE e.return_request_id = $1 AND e.kind = 'refunded'`, requestID).Scan(&events); err != nil {
		t.Fatalf("count timeline events after retry: %v", err)
	}
	if events != 0 {
		t.Errorf("%d refunded events after retrying a still-pending card, want 0", events)
	}
}

func TestTheRefundFigureCountsCreditToo(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	figures := reports.NewStore(pool)

	before, err := figures.Report(ctx, 30)
	if err != nil {
		t.Fatalf("read report before refund: %v", err)
	}
	requestID, orderNumber, _ := creditFundedReturn(t, 2, 200000)
	if decideErr := s.Decide(ctx, requestID.String(), "approved", "全額購物金", "", uuid.NullUUID{}); decideErr != nil {
		t.Fatalf("Decide: %v", decideErr)
	}
	after, err := figures.Report(ctx, 30)
	if err != nil {
		t.Fatalf("read report after refund: %v", err)
	}

	var card, credit int64
	if err := pool.QueryRow(ctx, `
		SELECT card_cents, credit_cents FROM order_refunds
		WHERE order_number = $1`, orderNumber).Scan(&card, &credit); err != nil {
		t.Fatalf("read the one refund definition: %v", err)
	}
	if card != 0 || credit != 200000 {
		t.Fatalf("order_refunds reads card=%d credit=%d, want 0/200000 — the fixture must be credit-only", card, credit)
	}
	if delta, want := after.RefundedCents-before.RefundedCents, card+credit; delta != want {
		t.Errorf("the report moved by %d while order_refunds says %d went back "+
			"(card %d + credit %d); both surfaces must read one definition", delta, want, card, credit)
	}
}

func TestCompensatingAReturnTwiceGivesCreditOnce(t *testing.T) {
	ctx, actor := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	requestID, _, accountID := creditFundedReturn(t, 2, 200000)

	if err := s.Decide(ctx, requestID.String(), "approved", "第一次", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("first Decide: %v", err)
	}
	// Retry the public compensation operation with the exact same authority.
	// Calling post_store_credit directly would now (correctly) be a different
	// attribution and must be rejected rather than mistaken for a replay.
	if _, err := pool.Exec(ctx, `
		SELECT compensate_return_with_credit($1, 200000, $2)`,
		requestID, actor); err != nil {
		t.Fatalf("second compensation: %v", err)
	}

	if got := creditBalanceOf(t, accountID); got != 200000 {
		t.Errorf("balance = %d after two compensations, want 200000 — the second must "+
			"have found the first by its key", got)
	}
}

func creditBalanceOf(t *testing.T, accountID uuid.UUID) int64 {
	t.Helper()
	var cents int64
	if err := pool.QueryRow(t.Context(),
		`SELECT coalesce(sum(amount_cents), 0) FROM store_credit_entries WHERE account_id = $1`,
		accountID).Scan(&cents); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return cents
}

func creditFundedReturn(t *testing.T, qty int32, creditCents int64) (
	requestID uuid.UUID, orderNumber string, accountID uuid.UUID,
) {
	t.Helper()
	return creditFundedReturnOn(t, pool, qty, creditCents)
}

func creditFundedReturnOn(
	t *testing.T, p *pgxpool.Pool, qty int32, creditCents int64,
) (requestID uuid.UUID, orderNumber string, accountID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	const price = 100000
	total := int64(2) * price
	card := total - creditCents

	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('ret-credit-'||gen_random_uuid()||'@goen.invalid', 'customer', '退貨額度')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT post_store_credit($1, $2, '測試發放', NULL, $3, NULL)`,
		userID, creditCents, "grant:"+userID.String()); err != nil {
		t.Fatalf("grant credit: %v", err)
	}

	var orderID, lineID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'REF-SKU', '測試商品', $2, 2) RETURNING id`,
		orderID, price).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'f@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	// The spend, while the order is still an open unpaid checkout: the only moment store_credit_guard allows it.
	if _, err := tx.Exec(ctx, `SELECT post_store_credit($1, $2, '訂單折抵', $3, $4, NULL)`,
		userID, -creditCents, orderID, "spend:"+orderID.String()); err != nil {
		t.Fatalf("spend credit: %v", err)
	}
	if card > 0 {
		if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`,
			orderID, "cs_cred_"+orderNumber, card); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`,
			"cs_cred_"+orderNumber, card); err != nil {
			t.Fatalf("capture: %v", err)
		}
	}

	moveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'black_cat', 'TC-'||$2) RETURNING id`,
		orderID, orderNumber).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 2)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason) VALUES ($1, '不合用') RETURNING id`,
		orderID).Scan(&requestID); err != nil {
		t.Fatalf("create return request: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, $4)`, orderID, requestID, lineID, qty); err != nil {
		t.Fatalf("create return line: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`SELECT id FROM store_credit_accounts WHERE user_id = $1`, userID).
		Scan(&accountID); err != nil {
		t.Fatalf("read credit account: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return requestID, orderNumber, accountID
}

func TestTheBackOfficeCanFindAnOrder(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number, recipient, addr := searchableOrder(t)

	for _, term := range []string{
		number,
		strings.ToLower(number),
		// Sliced by RUNE: recipient[:2] cuts a character in half and PostgreSQL refuses invalid UTF-8.
		string([]rune(recipient)[:2]),
		string([]rune(addr)[:8]),
		strings.ToUpper(string([]rune(addr)[:8])),
	} {
		view, err := s.Orders(ctx, "", term)
		if err != nil {
			t.Fatalf("Orders(%q): %v", term, err)
		}
		if !hasOrder(view, number) {
			t.Errorf("searching %q did not find %s", term, number)
		}
		if view.Term != term {
			t.Errorf("the box lost the term: %q", view.Term)
		}
	}
}

func TestASearchIgnoresTheStatusFilter(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number, _, _ := searchableOrder(t)

	view, err := s.Orders(ctx, "shipped", number)
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	if !hasOrder(view, number) {
		t.Error("a search was filtered away by the status tab")
	}
}

func TestATooShortSearchIsNotASearch(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	view, err := s.Orders(ctx, "", "王")
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	if view.Searching() {
		t.Error("a one-character term reads as a search")
	}
}

func TestAnErasedOrderIsNotFoundByItsOldAddress(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number, recipient, addr := searchableOrder(t)

	if _, err := pool.Exec(ctx, `
		UPDATE order_private_data pd SET
			email = NULL, recipient_name = NULL, phone = NULL, postal_code = NULL,
			city = NULL, district = NULL, street = NULL, erased_at = now()
		FROM orders o WHERE pd.order_id = o.id AND o.order_number = $1`,
		number); err != nil {
		t.Fatalf("erase: %v", err)
	}

	for _, term := range []string{string([]rune(recipient)[:2]), string([]rune(addr)[:8])} {
		view, err := s.Orders(ctx, "", term)
		if err != nil {
			t.Fatalf("Orders(%q): %v", term, err)
		}
		if hasOrder(view, number) {
			t.Errorf("an erased order was found by %q, which it no longer holds", term)
		}
	}
	view, err := s.Orders(ctx, "", number)
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	if !hasOrder(view, number) {
		t.Error("an erased order cannot be found by its own number; it is still an order")
	}
}

// TestAnOrderSearchTakesWildcardsLiterally: "%%" passes the two-rune floor, and
// a typed _ is part of an address, not a stand-in for any character.
func TestAnOrderSearchTakesWildcardsLiterally(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	underscored, _, _ := searchableOrder(t)
	lettered, _, _ := searchableOrder(t)
	stem := strings.ReplaceAll(uuid.NewString(), "-", "")
	for number, addr := range map[string]string{
		underscored: "lk_" + stem + "@goen.invalid",
		lettered:    "lkx" + stem + "@goen.invalid",
	} {
		if _, err := pool.Exec(ctx, `
			UPDATE order_private_data pd SET email = $2
			FROM orders o WHERE pd.order_id = o.id AND o.order_number = $1`, number, addr); err != nil {
			t.Fatalf("address order %s: %v", number, err)
		}
	}

	view, err := s.Orders(ctx, "", "%%")
	if err != nil {
		t.Fatalf("Orders(%%%%): %v", err)
	}
	if len(view.Orders) != 0 {
		t.Errorf(`searching "%%%%" listed %d orders; no address or name starts with it`, len(view.Orders))
	}

	view, err = s.Orders(ctx, "", "lk_"+stem)
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	if !hasOrder(view, underscored) {
		t.Errorf("searching the underscored address did not find %s", underscored)
	}
	if hasOrder(view, lettered) {
		t.Errorf("a typed _ matched %s, whose address has an x there", lettered)
	}
}

func hasOrder(v adminpages.OrdersView, number string) bool {
	for i := range v.Orders {
		if v.Orders[i].Number == number {
			return true
		}
	}
	return false
}

func searchableOrder(t *testing.T) (number, recipient, addr string) {
	t.Helper()
	ctx := t.Context()
	recipient = "尋" + uuid.NewString()[:6]
	addr = "search-" + strings.ReplaceAll(uuid.NewString(), "-", "") + "@goen.invalid"

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'SEARCH-SKU', '測試商品', 100000, 1)`, orderID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, $2, $3, '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID, addr, recipient); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, recipient, addr
}

// TestTheBackOfficeSeesTheSystemCancelAtThePaymentDeadline: the sweeper's
// cancellation is the system's, in both languages, and never the customer's.
func TestTheBackOfficeSeesTheSystemCancelAtThePaymentDeadline(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number := placeUnpaidOrderHolding(t, true)
	if _, err := pool.Exec(ctx, `
		UPDATE inventory_reservations
		SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 minute'
		WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`, number); err != nil {
		t.Fatalf("expire the hold: %v", err)
	}
	if _, _, err := cart.NewStore(pool).Sweep(ctx, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	var found bool
	for _, e := range view.Timeline {
		if e.Kind != "cancelled" {
			continue
		}
		found = true
		for locale, want := range map[i18n.Locale]string{i18n.ZhHant: "系統", i18n.En: "System"} {
			if got := e.By(i18n.WithLocale(ctx, locale)); got != want {
				t.Errorf("%s: the back office says %q cancelled it, want %q", locale, got, want)
			}
		}
	}
	if !found {
		t.Fatal("the sweep left no cancellation in the order's history")
	}
}

func TestTheBackOfficeSeesWhoCancelled(t *testing.T) {
	ctx, _ := staffContext(t)
	basket := cart.NewStore(pool)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number := placeUnpaidOrder(t)

	if _, err := basket.CancelOrder(ctx, number); err != nil {
		t.Fatalf("the customer cancels: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	var found bool
	for _, e := range view.Timeline {
		if e.Kind != "cancelled" {
			continue
		}
		found = true
		if e.By(ctx) != "顧客" {
			t.Errorf("the back office says %q cancelled it, want 顧客", e.By(ctx))
		}
		if e.Note != "" {
			t.Errorf("the cancellation still stores words: %q", e.Note)
		}
	}
	if !found {
		t.Fatal("no cancellation in the order's history")
	}
}

func TestADeliveredOrderMarksItsParcelsDelivered(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number := shippableOrder(t, "zh-Hant")

	if err := s.Ship(ctx, number, admin.Dispatch{Carrier: "black_cat", Tracking: "DELIVERED-" + number}, actor); err != nil {
		t.Fatalf("Ship: %v", err)
	}
	before, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order after shipping: %v", err)
	}
	if len(before.Shipments) != 1 {
		t.Fatalf("%d parcels after shipping, want 1", len(before.Shipments))
	}
	if before.Shipments[0].Delivered() {
		t.Error("a parcel that has just left reads as delivered")
	}

	if _, advErr := s.Advance(ctx, number, "delivered", actor); advErr != nil {
		t.Fatalf("Advance to delivered: %v", advErr)
	}
	after, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order after delivery: %v", err)
	}
	if !after.Shipments[0].Delivered() {
		t.Error("the order is delivered and its parcel still says otherwise")
	}

	first := deliveredAt(t, number)
	if first.IsZero() {
		t.Fatal("the parcel has no delivery timestamp at all")
	}
	if _, err := s.Advance(ctx, number, "completed", actor); err != nil {
		t.Fatalf("Advance to completed: %v", err)
	}
	if again := deliveredAt(t, number); !again.Equal(first) {
		t.Errorf("the delivery timestamp moved from %s to %s",
			first.Format(time.RFC3339Nano), again.Format(time.RFC3339Nano))
	}
}

func TestAnOrderCompletedWithoutADeliveryStepStillStampsItsParcels(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number := shippableOrder(t, "zh-Hant")

	if err := s.Ship(ctx, number, admin.Dispatch{Carrier: "black_cat", Tracking: "COLLECTED-" + number}, actor); err != nil {
		t.Fatalf("Ship: %v", err)
	}
	if !deliveredAt(t, number).IsZero() {
		t.Fatal("a parcel that has just left is already stamped delivered; the fixture is wrong")
	}

	if _, err := s.Advance(ctx, number, "completed", actor); err != nil {
		t.Fatalf("Advance to completed: %v", err)
	}

	if deliveredAt(t, number).IsZero() {
		t.Error("an order completed without a delivery step left its parcel unstamped — " +
			"the customer's page says it never arrived and /admin/returns reads the " +
			"rescission window as never having started")
	}
}

func TestShippingIsRefusedWhenTheOrderHoldsNoStock(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number, orderID := pickingOrderHoldingStock(t)

	if _, err := pool.Exec(ctx, `
		UPDATE inventory_reservations SET state = 'released', settled_at = now()
		WHERE order_id = $1 AND state = 'held'`, orderID); err != nil {
		t.Fatalf("strand the order: %v", err)
	}

	err := s.Ship(ctx, number, admin.Dispatch{Carrier: "black_cat", Tracking: "NOHOLD-" + number}, uuid.NullUUID{})
	if !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("shipping an order holding no stock = %v, want ErrRefused", err)
	}

	var shipments int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_shipments WHERE order_id = $1`, orderID).Scan(&shipments); err != nil {
		t.Fatalf("count shipments: %v", err)
	}
	if shipments != 0 {
		t.Errorf("%d parcels recorded against a refused dispatch, want 0", shipments)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "picking" {
		t.Errorf("order is %q after a refused dispatch, want picking", status)
	}
}

func deliveredAt(t *testing.T, number string) time.Time {
	t.Helper()
	var at *time.Time
	if err := pool.QueryRow(t.Context(), `
		SELECT sh.delivered_at FROM order_shipments sh
		JOIN orders o ON o.id = sh.order_id
		WHERE o.order_number = $1`, number).Scan(&at); err != nil {
		t.Fatalf("read the parcel: %v", err)
	}
	if at == nil {
		return time.Time{}
	}
	return *at
}

func TestTheShopCanGiveAProductASpecTable(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := draftProduct(t, ctx, s)

	if errs, err := s.AddSpec(ctx, slug, admin.SpecDraft{
		Label: "螢幕", Value: "6.3 吋 OLED", LabelEn: "Screen",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("AddSpec: %v %v", err, errs)
	}
	if errs, err := s.AddSpec(ctx, slug, admin.SpecDraft{
		Label: "重量", Value: "187 公克", LabelEn: "Weight", ValueEn: "187 g",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("AddSpec: %v %v", err, errs)
	}

	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if !view.HasSpecs() {
		t.Fatal("the product states no specs after two were added")
	}
	want := []string{"螢幕", "重量"}
	got := make([]string, 0, len(view.Specs))
	for _, sp := range view.Specs {
		got = append(got, sp.Label)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("spec labels (-want +got):\n%s", diff)
	}

	if rmErr := s.RemoveSpec(ctx, slug, view.Specs[0].ID); rmErr != nil {
		t.Fatalf("RemoveSpec: %v", rmErr)
	}
	after, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product after removal: %v", err)
	}
	if len(after.Specs) != 1 || after.Specs[0].Label != "重量" {
		t.Errorf("after removing 螢幕 the table is %+v", after.Specs)
	}

	other := draftProduct(t, ctx, s)
	if rmErr := s.RemoveSpec(ctx, other, after.Specs[0].ID); !errors.Is(rmErr, admin.ErrNotFound) {
		t.Errorf("removing another product's spec returned %v, want ErrNotFound", rmErr)
	}
}

func TestASpecIsRefusedRatherThanTruncated(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := draftProduct(t, ctx, s)

	tests := []struct {
		name  string
		label string
		value string
		field string
	}{
		{name: "no label", label: "  ", value: "6.3 吋", field: "spec_label"},
		{name: "no value", label: "螢幕", value: "\t", field: "spec_value"},
		{
			name:  "a label that is a sentence",
			label: strings.Repeat("螢", admin.SpecLabelRunes+1),
			value: "6.3 吋",
			field: "spec_label",
		},
		{
			name:  "a value past the bound",
			label: "螢幕",
			value: strings.Repeat("吋", admin.SpecValueRunes+1),
			field: "spec_value",
		},
	}
	if errs, _ := s.AddSpec(ctx, slug, admin.SpecDraft{
		Label: "螢幕", Value: "6.3 吋",
		LabelEn: strings.Repeat("S", admin.SpecLabelRunes+1),
	}); errs["spec_label_en"] == "" {
		t.Errorf("an over-long English label was accepted: %v", errs)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs, err := s.AddSpec(ctx, slug, admin.SpecDraft{
				Label: tt.label, Value: tt.value,
			})
			if err != nil {
				t.Fatalf("AddSpec: %v", err)
			}
			if _, ok := errs[tt.field]; !ok {
				t.Errorf("AddSpec(%q, %q) refused %v, want a %s error",
					tt.label, tt.value, errs, tt.field)
			}
		})
	}

	if errs, err := s.AddSpec(ctx, slug, admin.SpecDraft{
		Label: strings.Repeat("螢", admin.SpecLabelRunes), Value: "剛好",
	}); err != nil || len(errs) > 0 {
		t.Errorf("a label exactly at the bound was refused: %v %v", err, errs)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO product_specs (product_id, label, value, position)
		SELECT id, repeat('螢', 41), '繞過表單', 99 FROM products WHERE slug = $1`, slug)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok ||
		pgErr.ConstraintName != "product_specs_label_bounded" {
		t.Errorf("a 41-character label written directly returned %v, want "+
			"product_specs_label_bounded", err)
	}
}

func TestAProductCannotHaveTwoSpecsWithTheSameIdentity(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := draftProduct(t, ctx, s)

	if errs, err := s.AddSpec(ctx, slug, admin.SpecDraft{
		Label: "連接埠", Value: "USB-C",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("first AddSpec: %v %v", err, errs)
	}
	errs, err := s.AddSpec(ctx, slug, admin.SpecDraft{
		Label: "連接埠", Value: "HDMI",
	})
	if err != nil {
		t.Fatalf("duplicate AddSpec returned an infrastructure error: %v", err)
	}
	if errs["spec_label"] == "" {
		t.Fatalf("duplicate AddSpec errors = %v, want spec_label", errs)
	}

	var rows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM product_specs s
		JOIN products p ON p.id = s.product_id
		WHERE p.slug = $1 AND s.label = '連接埠'`, slug).Scan(&rows); err != nil {
		t.Fatalf("count duplicate specs: %v", err)
	}
	if rows != 1 {
		t.Errorf("product has %d specs named 連接埠, want 1; Compare would overwrite a cell", rows)
	}
}

func draftProduct(t *testing.T, ctx context.Context, s *admin.Store) string {
	t.Helper()

	var brandID, catID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM brands LIMIT 1`).Scan(&brandID); err != nil {
		t.Fatalf("read a brand: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM categories LIMIT 1`).Scan(&catID); err != nil {
		t.Fatalf("read a category: %v", err)
	}
	slug := "spec-" + uuid.New().String()[:8]
	form := &admin.ProductForm{
		Slug: slug, Name: "規格表測試 " + slug, Summary: "測試用",
		Description: "測試用商品", BrandID: brandID, CategoryID: catID,
	}
	created, errs, err := s.CreateProduct(ctx, form)
	if err != nil || len(errs) > 0 {
		t.Fatalf("CreateProduct: %v %v", err, errs)
	}
	return created
}

func TestTheShopCanGiveAProductAVariantPicker(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := draftProduct(t, ctx, s)

	if errs, err := s.AddOption(ctx, slug, admin.OptionDraft{
		Name: "顏色", NameEn: "Colour",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", err, errs)
	}
	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if !view.HasOptions() || view.Options[0].Name != "顏色" {
		t.Fatalf("the product's options are %+v", view.Options)
	}
	if view.Options[0].HasValues() {
		t.Error("a new option already has values")
	}

	optionID := view.Options[0].ID
	for _, v := range []struct{ value, label string }{
		{"星霧藍", "Mist Blue"},
		{"曜石黑", ""},
	} {
		if errs, addErr := s.AddOptionValue(ctx, slug, admin.OptionDraft{
			OptionID: optionID, Name: v.value, NameEn: v.label,
		}); addErr != nil || len(errs) > 0 {
			t.Fatalf("AddOptionValue(%s): %v %v", v.value, addErr, errs)
		}
	}

	view, err = s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if len(view.Options[0].Values) != 2 {
		t.Fatalf("the axis has %d values, want 2", len(view.Options[0].Values))
	}
	if view.Options[0].Values[0].Value != "星霧藍" ||
		view.Options[0].Values[0].Label != "Mist Blue" {
		t.Errorf("the first value is %+v", view.Options[0].Values[0])
	}
	if view.Options[0].Values[1].Label != "" {
		t.Errorf("an untranslated value reports label %q",
			view.Options[0].Values[1].Label)
	}

	if errs, addErr := s.AddVariant(ctx, slug, &admin.VariantForm{
		SKU: "PICKER-NONE", PriceCents: 100000,
	}); addErr != nil {
		t.Fatalf("AddVariant: %v", addErr)
	} else if errs["options"] == "" {
		t.Errorf("a variant naming no option value was accepted: %v", errs)
	}

	if errs, addErr := s.AddVariant(ctx, slug, &admin.VariantForm{
		SKU: "PICKER-BLUE", PriceCents: 100000,
		OptionValues: []string{view.Options[0].Values[0].ID},
	}); addErr != nil || len(errs) > 0 {
		t.Fatalf("AddVariant: %v %v", addErr, errs)
	}

	after, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	var found bool
	for _, sv := range after.Variants {
		if sv.SKU != "PICKER-BLUE" {
			continue
		}
		found = true
		if sv.OptionText() != "星霧藍" {
			t.Errorf("the variant list shows %q for its options", sv.OptionText())
		}
	}
	if !found {
		t.Error("the new variant is not in the product's variant list")
	}
}

// A colour is a shape, not a word, and the shop finds that out at the field
// rather than from a refused write. The column's CHECK is the last word; this
// is the first one, and the two have to agree or the page 500s on a typo.
func TestAMistypedColourComesBackBesideTheField(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	h := adminHandlerOver(pool, s)
	slug := draftProduct(t, ctx, s)

	if errs, err := s.AddOption(ctx, slug, admin.OptionDraft{
		Name: "顏色", NameEn: "Colour",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", err, errs)
	}
	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	optionID := view.Options[0].ID

	refused := []struct {
		name string
		raw  string
	}{
		{name: "no hash", raw: "1c1c1e"},
		{name: "three digits", raw: "#abc"},
		{name: "not hexadecimal", raw: "#1c1c1g"},
		{name: "a colour name", raw: "black"},
		{name: "too long", raw: "#1c1c1e0"},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			res := postOptionValueForm(t, h, ctx, slug, url.Values{
				"option":     {optionID},
				"value":      {"色碼測試 " + uuid.NewString()[:8]},
				"value_en":   {""},
				"swatch_hex": {tt.raw},
			})
			if res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("AddOptionValue(swatch_hex=%q) status = %d, want 422", tt.raw, res.Code)
			}
			body := res.Body.String()
			input := admintest.InputElementByID(t, body, "optval-swatch")
			if got := admintest.InputAttribute(t, input, "aria-invalid"); got != "true" {
				t.Errorf("colour field aria-invalid = %q, want true", got)
			}
			if got := admintest.InputAttribute(t, input, "aria-describedby"); got != "optval-swatch-error" {
				t.Errorf("colour field aria-describedby = %q, want the error's id", got)
			}
			if !regexp.MustCompile(`<p[^>]*id="optval-swatch-error"[^>]*>[^<]+</p>`).MatchString(body) {
				t.Error("the refusal names no reason beside the colour field")
			}
			if strings.Contains(admintest.InputElementByID(t, body, "optval-value"), `aria-invalid`) {
				t.Error("a bad colour marked the value field invalid too")
			}
		})
	}

	// The spelling is not the shape. A shop that types the other case is
	// storing the same colour, so this one is accepted and lower-cased.
	value := "曜石黑 " + uuid.NewString()[:8]
	res := postOptionValueForm(t, h, ctx, slug, url.Values{
		"option":     {optionID},
		"value":      {value},
		"value_en":   {""},
		"swatch_hex": {"#1C1C1E"},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("AddOptionValue(swatch_hex=%q) status = %d, want 303", "#1C1C1E", res.Code)
	}
	var stored string
	if err := pool.QueryRow(ctx,
		`SELECT swatch_hex FROM product_option_values WHERE option_id = $1::uuid AND value = $2`,
		optionID, value).Scan(&stored); err != nil {
		t.Fatalf("read the stored colour: %v", err)
	}
	if stored != "#1c1c1e" {
		t.Errorf("stored colour = %q, want the lower-cased spelling", stored)
	}
}

func postOptionValueForm(
	t *testing.T, h *admin.Handler, ctx context.Context, slug string, form url.Values,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/products/"+slug+"/options/values", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	res := httptest.NewRecorder()
	h.AddOptionValue(res, req)
	return res
}

func TestAVariantCannotBorrowAnotherProductsOptionValue(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	first := draftProduct(t, ctx, s)
	if errs, err := s.AddOption(ctx, first, admin.OptionDraft{Name: "顏色"}); err != nil ||
		len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", err, errs)
	}
	firstView, err := s.Product(ctx, first)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if errs, addErr := s.AddOptionValue(ctx, first, admin.OptionDraft{
		OptionID: firstView.Options[0].ID, Name: "星霧藍",
	}); addErr != nil || len(errs) > 0 {
		t.Fatalf("AddOptionValue: %v %v", addErr, errs)
	}
	firstView, err = s.Product(ctx, first)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	borrowed := firstView.Options[0].Values[0].ID

	second := draftProduct(t, ctx, s)
	if errs, addErr := s.AddOption(ctx, second, admin.OptionDraft{Name: "顏色"}); addErr != nil ||
		len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", addErr, errs)
	}

	errs, addErr := s.AddVariant(ctx, second, &admin.VariantForm{
		SKU: "BORROW-1", PriceCents: 100000, OptionValues: []string{borrowed},
	})
	if addErr != nil {
		t.Fatalf("AddVariant: %v", addErr)
	}
	if errs["options"] == "" {
		t.Errorf("a variant borrowing another product's value was accepted: %v", errs)
	}
	var variants int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM product_variants WHERE sku = 'BORROW-1'`).Scan(&variants); err != nil {
		t.Fatalf("count variants: %v", err)
	}
	if variants != 0 {
		t.Errorf("%d variants named BORROW-1 survived the refusal", variants)
	}
}

func TestTheOptionValueIsAddedToTheRightProduct(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	first := draftProduct(t, ctx, s)
	if errs, err := s.AddOption(ctx, first, admin.OptionDraft{Name: "顏色"}); err != nil ||
		len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", err, errs)
	}
	view, err := s.Product(ctx, first)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}

	second := draftProduct(t, ctx, s)
	errs, err := s.AddOptionValue(ctx, second, admin.OptionDraft{
		OptionID: view.Options[0].ID, Name: "星霧藍",
	})
	if err != nil {
		t.Fatalf("AddOptionValue: %v", err)
	}
	if errs["value"] == "" {
		t.Errorf("a value was added to another product's axis: %v", errs)
	}
}

func TestAltTextFollowsThePagesLanguage(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := draftProduct(t, ctx, s)
	digest := storeMedia(t)

	if err := s.AttachImage(ctx, slug, digest, "銀色筆電,螢幕開啟",
		"Silver laptop, screen open", "", 800, 600); err != nil {
		t.Fatalf("AttachImage: %v", err)
	}

	for _, tt := range []struct {
		name   string
		locale string
		want   string
	}{
		{name: "a Chinese page", locale: "zh-Hant", want: "銀色筆電,螢幕開啟"},
		{name: "an English page", locale: "en", want: "Silver laptop, screen open"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			if err := pool.QueryRow(ctx, `
				SELECT localized_name(i.alt_text, i.alt_text_en, $2)
				FROM product_images i JOIN products p ON p.id = i.product_id
				WHERE p.slug = $1`, slug, tt.locale).Scan(&got); err != nil {
				t.Fatalf("read the alt text: %v", err)
			}
			if got != tt.want {
				t.Errorf("the %s announces %q, want %q", tt.name, got, tt.want)
			}
		})
	}

	second := draftProduct(t, ctx, s)
	if err := s.AttachImage(ctx, second, digest, "沒有英文說明", "", "", 800, 600); err != nil {
		t.Fatalf("AttachImage without English: %v", err)
	}
	var fallback string
	if err := pool.QueryRow(ctx, `
		SELECT localized_name(i.alt_text, i.alt_text_en, 'en')
		FROM product_images i JOIN products p ON p.id = i.product_id
		WHERE p.slug = $1`, second).Scan(&fallback); err != nil {
		t.Fatalf("read the alt text: %v", err)
	}
	if fallback != "沒有英文說明" {
		t.Errorf("an untranslated image announces %q, want the Chinese text", fallback)
	}
}

func storeMedia(t *testing.T) string {
	t.Helper()
	digest := fmt.Sprintf("%064x", uuid.New().ID())
	if _, err := pool.Exec(t.Context(), `
		-- byte_size must equal length(bytes): media_objects_size_matches refuses a
		-- row that claims a size its own bytes do not have.
		INSERT INTO media_objects (digest, content_type, byte_size, width, height, bytes)
		VALUES ($1, 'image/png', 1, 800, 600, '\x00'::bytea)`, digest); err != nil {
		t.Fatalf("store the image: %v", err)
	}
	return digest
}

const (
	invoiceFAQQuestion = "發票怎麼開立?"
	invoiceFAQZh       = "結帳時可以選擇會員載具、手機條碼載具或公司統編,付款完成時系統會依您的選擇自動開立電子發票。這份部署若尚未設定綠界加值中心則不會開立,後台會說明原因。"
	invoiceFAQEn       = "At checkout you can choose a member carrier, a mobile barcode carrier, or a company tax ID, and the electronic invoice is issued automatically against that choice when your payment completes. Without ECPay credentials this deployment files nothing, and the back office says so."
	staleInvoiceFAQZh  = "結帳時可以選擇會員載具、手機條碼載具或公司統編,系統會記錄您的選擇。電子發票的實際開立需要串接加值中心,這部分尚未完成。"
	staleInvoiceFAQEn  = "At checkout you can choose a member carrier, a mobile barcode carrier, or a company tax ID, and we record your choice. Actually issuing the electronic invoice needs an integration with a certified provider, which is not built yet."
)

// TestSeededInvoiceFAQMatchesTheWiredIssuer holds the published invoice FAQ
// to Gateway.Issue: a fresh seed, the shipped repair file on a kept stale
// row, and the back-office form all say the same deployment-gated thing,
// in both locales.
func TestSeededInvoiceFAQMatchesTheWiredIssuer(t *testing.T) {
	ctx, _ := staffContext(t)
	siteStore := site.NewStore(pool)

	if _, err := invoice.NewGateway("", "", "", ""); err != nil {
		t.Fatalf("an empty configuration must be legal: %v", err)
	}

	assertInvoiceFAQLocales(t, siteStore, ctx, invoiceFAQZh, invoiceFAQEn)
	assertStatutoryReturnFAQUntouched(t, siteStore, ctx)

	catalog, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatalf("read catalogue seed: %v", err)
	}
	repair, err := os.ReadFile("../../seed/repair_invoice_faq.sql")
	if err != nil {
		t.Fatalf("read shipped invoice FAQ repair: %v", err)
	}

	isolated := dbtest.Pool(t)
	if _, loadErr := isolated.Exec(ctx, string(catalog)); loadErr != nil {
		t.Fatalf("seed isolated catalogue: %v", loadErr)
	}
	plantStaleInvoiceFAQ(t, ctx, isolated)
	isolatedContent := site.NewStore(isolated)
	zh, en := invoiceFAQAnswers(t, isolatedContent, ctx)
	if !strings.Contains(zh, "尚未完成") || !strings.Contains(en, "not built yet") {
		t.Fatalf("the planted stale answers did not reach /faq: zh=%q en=%q", zh, en)
	}
	conn, err := isolated.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire isolated connection: %v", err)
	}
	if _, seedErr := conn.Exec(ctx, string(catalog)); seedErr == nil {
		conn.Release()
		t.Fatal("the catalogue seed succeeded against a kept database; " +
			"db-seed cannot be the repair path")
	}
	if _, rollErr := conn.Exec(ctx, "ROLLBACK"); rollErr != nil {
		conn.Release()
		t.Fatalf("rollback failed catalogue re-seed: %v", rollErr)
	}
	conn.Release()
	zh, en = invoiceFAQAnswers(t, isolatedContent, ctx)
	if !strings.Contains(zh, "尚未完成") || !strings.Contains(en, "not built yet") {
		t.Fatal("the failed catalogue re-seed changed the kept invoice FAQ")
	}
	if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
		t.Fatalf("apply shipped invoice FAQ repair: %v", repairErr)
	}
	assertInvoiceFAQLocales(t, isolatedContent, ctx, invoiceFAQZh, invoiceFAQEn)
	assertStatutoryReturnFAQUntouched(t, isolatedContent, ctx)

	t.Cleanup(func() {
		if _, restoreErr := pool.Exec(context.WithoutCancel(ctx), string(repair)); restoreErr != nil {
			t.Errorf("restore invoice FAQ: %v", restoreErr)
		}
	})
	plantStaleInvoiceFAQ(t, ctx, pool)
	var entry adminpages.FAQEntry
	view, err := contentdesk.NewStore(pool).FAQ(ctx)
	if err != nil {
		t.Fatalf("FAQ: %v", err)
	}
	for i := range view.Rows {
		if view.Rows[i].Question == invoiceFAQQuestion {
			entry = view.Rows[i]
			break
		}
	}
	if entry.ID == "" {
		t.Fatal("the invoice FAQ row is not in the back office")
	}
	if errs, updErr := contentdesk.NewStore(pool).UpdateFAQEntry(ctx, &contentdesk.FAQForm{
		ID: entry.ID, Category: entry.Category,
		Question: invoiceFAQQuestion, Answer: invoiceFAQZh,
		CategoryEn: entry.CategoryEn, QuestionEn: entry.QuestionEn,
		AnswerEn: invoiceFAQEn,
	}); updErr != nil || len(errs) > 0 {
		t.Fatalf("UpdateFAQEntry: %v %v", updErr, errs)
	}
	assertInvoiceFAQLocales(t, siteStore, ctx, invoiceFAQZh, invoiceFAQEn)
	assertStatutoryReturnFAQUntouched(t, siteStore, ctx)
}

// TestRepairInvoiceFAQPreservesEditedLocales runs the shipped repair file against
// kept rows and rewrites only a locale that still exactly matches the stale copy.
func TestRepairInvoiceFAQPreservesEditedLocales(t *testing.T) {
	ctx := t.Context()
	catalog, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatalf("read catalogue seed: %v", err)
	}
	repair, err := os.ReadFile("../../seed/repair_invoice_faq.sql")
	if err != nil {
		t.Fatalf("read shipped invoice FAQ repair: %v", err)
	}

	cases := []struct {
		name           string
		zh, en         string
		wantZh, wantEn string
	}{
		{
			name: "both stale",
			zh:   staleInvoiceFAQZh, en: staleInvoiceFAQEn,
			wantZh: invoiceFAQZh, wantEn: invoiceFAQEn,
		},
		{
			name: "custom Chinese stale English",
			zh:   "店家自訂發票說明", en: staleInvoiceFAQEn,
			wantZh: "店家自訂發票說明", wantEn: invoiceFAQEn,
		},
		{
			name: "stale Chinese custom English",
			zh:   staleInvoiceFAQZh, en: "Merchant invoice instructions",
			wantZh: invoiceFAQZh, wantEn: "Merchant invoice instructions",
		},
		{
			name: "both custom",
			zh:   "店家自訂發票說明", en: "Merchant invoice instructions",
			wantZh: "店家自訂發票說明", wantEn: "Merchant invoice instructions",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolated := dbtest.Pool(t)
			if _, loadErr := isolated.Exec(ctx, string(catalog)); loadErr != nil {
				t.Fatalf("seed catalogue: %v", loadErr)
			}
			if _, plantErr := isolated.Exec(ctx, `
				UPDATE faq_entries SET answer = $1, answer_en = $2
				WHERE question = $3`, tc.zh, tc.en, invoiceFAQQuestion); plantErr != nil {
				t.Fatalf("plant invoice FAQ: %v", plantErr)
			}
			if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
				t.Fatalf("apply shipped invoice FAQ repair: %v", repairErr)
			}
			content := site.NewStore(isolated)
			gotZh, gotEn := invoiceFAQAnswers(t, content, ctx)
			if gotZh != tc.wantZh {
				t.Errorf("Chinese = %q, want %q", gotZh, tc.wantZh)
			}
			if gotEn != tc.wantEn {
				t.Errorf("English = %q, want %q", gotEn, tc.wantEn)
			}
			assertStatutoryReturnFAQUntouched(t, content, ctx)
		})
	}

	t.Run("absent row", func(t *testing.T) {
		isolated := dbtest.Pool(t)
		if _, loadErr := isolated.Exec(ctx, string(catalog)); loadErr != nil {
			t.Fatalf("seed catalogue: %v", loadErr)
		}
		if _, delErr := isolated.Exec(ctx,
			`DELETE FROM faq_entries WHERE question = $1`, invoiceFAQQuestion); delErr != nil {
			t.Fatalf("delete invoice FAQ: %v", delErr)
		}
		if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
			t.Fatalf("apply shipped invoice FAQ repair: %v", repairErr)
		}
		var count int
		if err := isolated.QueryRow(ctx,
			`SELECT count(*) FROM faq_entries WHERE question = $1`, invoiceFAQQuestion).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 0 {
			t.Error("the repair recreated the invoice FAQ row")
		}
		content := site.NewStore(isolated)
		assertStatutoryReturnFAQUntouched(t, content, ctx)
	})
}

func plantStaleInvoiceFAQ(t *testing.T, ctx context.Context, p *pgxpool.Pool) {
	t.Helper()
	if _, err := p.Exec(ctx, `
		UPDATE faq_entries
		SET answer = $1, answer_en = $2
		WHERE question = $3`,
		staleInvoiceFAQZh, staleInvoiceFAQEn, invoiceFAQQuestion); err != nil {
		t.Fatalf("plant stale invoice FAQ: %v", err)
	}
}

func invoiceFAQAnswers(t *testing.T, content *site.Store, ctx context.Context) (zh, en string) {
	t.Helper()
	return faqAnswer(t, content, ctx, i18n.ZhHant, invoiceFAQQuestion),
		faqAnswer(t, content, ctx, i18n.En, "How is my invoice issued?")
}

func faqAnswer(t *testing.T, content *site.Store, ctx context.Context, loc i18n.Locale, question string) string {
	t.Helper()
	rows, err := content.FAQEntries(i18n.WithLocale(ctx, loc))
	if err != nil {
		t.Fatalf("FAQEntries(%s): %v", loc, err)
	}
	for i := range rows {
		if rows[i].Question == question {
			return rows[i].Answer
		}
	}
	t.Fatalf("/faq has no %q in %s", question, loc)
	return ""
}

func assertInvoiceFAQLocales(t *testing.T, content *site.Store, ctx context.Context, wantZh, wantEn string) {
	t.Helper()
	zh, en := invoiceFAQAnswers(t, content, ctx)
	if zh != wantZh {
		t.Errorf("Chinese invoice FAQ = %q, want %q", zh, wantZh)
	}
	if en != wantEn {
		t.Errorf("English invoice FAQ = %q, want %q", en, wantEn)
	}
	if strings.Contains(zh, "尚未完成") || strings.Contains(en, "not built yet") {
		t.Errorf("published invoice FAQ still calls the issuer unfinished: zh=%q en=%q", zh, en)
	}
	if strings.Contains(zh, "已成功開立") || strings.Contains(en, "successfully issued") {
		t.Errorf("published invoice FAQ invents a successful filing: zh=%q en=%q", zh, en)
	}
	if !strings.Contains(zh, "綠界") || !strings.Contains(en, "ECPay") {
		t.Errorf("published invoice FAQ does not name the issuer: zh=%q en=%q", zh, en)
	}
}

func assertStatutoryReturnFAQUntouched(t *testing.T, content *site.Store, ctx context.Context) {
	t.Helper()
	zh := faqAnswer(t, content, ctx, i18n.ZhHant, "退貨要付運費嗎?")
	en := faqAnswer(t, content, ctx, i18n.En, "Who pays return postage?")
	if !strings.Contains(zh, "退貨運費由 goen 負擔") {
		t.Errorf("statutory return FAQ was edited: %q", zh)
	}
	if !strings.Contains(en, "Rescinding within seven days") {
		t.Errorf("statutory English return FAQ was edited: %q", en)
	}
}

const (
	refundFAQQuestion = "退款什麼時候會收到?"
	refundFAQZh       = "退貨經審核同意後,系統依原付款組成退回:卡款立刻向 Stripe 發出退款,店儲退回購物金。卡款入帳時間依發卡銀行而定,通常是數個工作天;額度退回後可立刻使用。"
	refundFAQEn       = "As soon as a return is approved we pay it back the way you paid: the card share through Stripe, store credit back to your balance. When a card refund lands depends on your card issuer, usually a few working days; credit is available again at once."
	staleRefundFAQZh  = "退貨經審核同意後,系統會立即向 Stripe 發出退款。實際入帳時間依發卡銀行而定,通常是數個工作天。"
	staleRefundFAQEn  = "As soon as a return is approved we ask Stripe to refund. When it lands depends on your card issuer, usually a few working days."
	customRefundFAQZh = "店家自訂退款說明"
	customRefundFAQEn = "Shop-edited refund note"
)

// TestAShippedRefundFAQRepairRewritesOnlyStaleLocales holds the published
// refund FAQ to the original payment composition: a fresh seed, the shipped
// repair file on a kept stale row, and each locale matched on its own
// Stripe-only sentence so a shop-edited answer stays.
func TestAShippedRefundFAQRepairRewritesOnlyStaleLocales(t *testing.T) {
	ctx := t.Context()
	content := site.NewStore(pool)
	assertRefundFAQLocales(t, content, ctx, refundFAQZh, refundFAQEn)

	catalog, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatalf("read catalogue seed: %v", err)
	}
	repair, err := os.ReadFile("../../seed/repair_refund_faq.sql")
	if err != nil {
		t.Fatalf("read shipped refund FAQ repair: %v", err)
	}

	isolated := dbtest.Pool(t)
	if _, loadErr := isolated.Exec(ctx, string(catalog)); loadErr != nil {
		t.Fatalf("seed isolated catalogue: %v", loadErr)
	}
	plantRefundFAQ(t, ctx, isolated, staleRefundFAQZh, staleRefundFAQEn)
	isolatedContent := site.NewStore(isolated)
	zh, en := refundFAQAnswers(t, isolatedContent, ctx)
	if zh != staleRefundFAQZh || en != staleRefundFAQEn {
		t.Fatalf("the planted stale answers did not reach /faq: zh=%q en=%q", zh, en)
	}

	conn, err := isolated.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire isolated connection: %v", err)
	}
	if _, seedErr := conn.Exec(ctx, string(catalog)); seedErr == nil {
		conn.Release()
		t.Fatal("the catalogue seed succeeded against a kept database; " +
			"db-seed cannot be the repair path")
	}
	if _, rollErr := conn.Exec(ctx, "ROLLBACK"); rollErr != nil {
		conn.Release()
		t.Fatalf("rollback failed catalogue re-seed: %v", rollErr)
	}
	conn.Release()
	zh, en = refundFAQAnswers(t, isolatedContent, ctx)
	if zh != staleRefundFAQZh || en != staleRefundFAQEn {
		t.Fatal("the failed catalogue re-seed changed the kept refund FAQ")
	}

	if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
		t.Fatalf("apply shipped refund FAQ repair: %v", repairErr)
	}
	assertRefundFAQLocales(t, isolatedContent, ctx, refundFAQZh, refundFAQEn)

	plantRefundFAQ(t, ctx, isolated, customRefundFAQZh, staleRefundFAQEn)
	if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
		t.Fatalf("apply shipped refund FAQ repair after a Chinese edit: %v", repairErr)
	}
	assertRefundFAQLocales(t, isolatedContent, ctx, customRefundFAQZh, refundFAQEn)

	plantRefundFAQ(t, ctx, isolated, staleRefundFAQZh, customRefundFAQEn)
	if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
		t.Fatalf("apply shipped refund FAQ repair after an English edit: %v", repairErr)
	}
	assertRefundFAQLocales(t, isolatedContent, ctx, refundFAQZh, customRefundFAQEn)
}

func plantRefundFAQ(t *testing.T, ctx context.Context, p *pgxpool.Pool, zh, en string) {
	t.Helper()
	if _, err := p.Exec(ctx, `
		UPDATE faq_entries
		SET answer = $1, answer_en = $2
		WHERE question = $3`, zh, en, refundFAQQuestion); err != nil {
		t.Fatalf("plant refund FAQ: %v", err)
	}
}

func refundFAQAnswers(t *testing.T, content *site.Store, ctx context.Context) (zh, en string) {
	t.Helper()
	return faqAnswer(t, content, ctx, i18n.ZhHant, refundFAQQuestion),
		faqAnswer(t, content, ctx, i18n.En, "When will I get my refund?")
}

func assertRefundFAQLocales(t *testing.T, content *site.Store, ctx context.Context, wantZh, wantEn string) {
	t.Helper()
	zh, en := refundFAQAnswers(t, content, ctx)
	if zh != wantZh {
		t.Errorf("Chinese refund FAQ = %q, want %q", zh, wantZh)
	}
	if en != wantEn {
		t.Errorf("English refund FAQ = %q, want %q", en, wantEn)
	}
}

func discountedProductSlugs(t *testing.T, n int) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT DISTINCT p.slug FROM products p JOIN product_variants pv ON pv.product_id = p.id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.compare_at_price_cents > pv.price_cents
		ORDER BY p.slug
		LIMIT $1`, n)
	if err != nil {
		t.Fatalf("find discounted products: %v", err)
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var slug string
		if scanErr := rows.Scan(&slug); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		slugs = append(slugs, slug)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate discounted products: %v", err)
	}
	if len(slugs) < n {
		t.Fatalf("found %d discounted products, need %d", len(slugs), n)
	}
	return slugs
}

// TestTwoConcurrentCampaignFeaturesTakeDistinctPositions proves the advisory lock
// is in the statement: without it two staff members featuring at once both read
// the same max(position) and sale_campaign_products_position_key refuses one.
func TestTwoConcurrentCampaignFeaturesTakeDistinctPositions(t *testing.T) {
	ctx, _ := staffContext(t)
	slug := admintest.CampaignSlug(t)
	if _, err := campaigns.NewStore(pool).Create(ctx, &campaigns.Form{
		Slug: slug, Title: "併發活動", Days: 7,
	}); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM sale_campaigns WHERE slug = $1`, slug)
	})
	products := discountedProductSlugs(t, 2)

	poolA := admintest.NamedPool(t, pool, "campaign-append-a-"+uuid.NewString()[:8])
	poolB := admintest.NamedPool(t, pool, "campaign-append-b-"+uuid.NewString()[:8])
	storeA := campaigns.NewStore(poolA)
	storeB := campaigns.NewStore(poolB)

	start := make(chan struct{})
	type result struct {
		product string
		err     error
	}
	done := make(chan result, 2)
	go func() {
		<-start
		done <- result{product: products[0], err: storeA.FeatureProduct(ctx, slug, products[0])}
	}()
	go func() {
		<-start
		done <- result{product: products[1], err: storeB.FeatureProduct(ctx, slug, products[1])}
	}()
	close(start)

	featured := make([]string, 0, 2)
	for range 2 {
		got := <-done
		if got.err != nil {
			t.Fatalf("FeatureProduct(%s): %v", got.product, got.err)
		}
		featured = append(featured, got.product)
	}

	var positions []int32
	rows, err := pool.Query(ctx, `
		SELECT cp.position
		FROM sale_campaign_products cp
		JOIN sale_campaigns c ON c.id = cp.campaign_id
		JOIN products p ON p.id = cp.product_id
		WHERE c.slug = $1 AND p.slug = ANY($2)
		ORDER BY cp.position`, slug, featured)
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p int32
		if scanErr := rows.Scan(&p); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		positions = append(positions, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate campaign positions: %v", err)
	}
	if len(positions) != 2 || positions[0] == positions[1] {
		t.Errorf("positions = %v, want two distinct values — the lock is not in "+
			"the statement, so two concurrent features collided on max(position)+1",
			positions)
	}
}

func postVariantForm(
	t *testing.T, h *admin.Handler, ctx context.Context, slug string, form url.Values,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/products/"+slug+"/variants", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	res := httptest.NewRecorder()
	h.AddVariant(res, req)
	return res
}

// TestAReleaseInTheLedgerNamesItsOrder drives a RELEASE, the only movement that reaches
// the order through the reservation: a HOLD stays green with that join deleted.
func TestAReleaseInTheLedgerNamesItsOrder(t *testing.T) {
	ctx, _ := staffContext(t)
	basket := cart.NewStore(pool)

	var vid uuid.UUID
	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT pv.id, pv.sku FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.stock_quantity > pv.safety_stock + 2
		ORDER BY pv.stock_quantity DESC LIMIT 1`).Scan(&vid, &sku); err != nil {
		t.Fatalf("find a stocked variant: %v", err)
	}
	number := placeHeldOrder(t, vid)

	if _, err := basket.CancelOrder(ctx, number); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	view, err := stock.NewStore(pool).Movements(ctx, sku)
	if err != nil {
		t.Fatalf("Movements: %v", err)
	}
	var release, hold bool
	for i := range view.Rows {
		m := &view.Rows[i]
		if m.OrderNumber != number {
			continue
		}
		switch m.Reason {
		case "release":
			release = true
		case "hold":
			hold = true
		}
	}
	if !release {
		seen := make([]string, 0, len(view.Rows))
		for i := range view.Rows {
			seen = append(seen, view.Rows[i].Reason+"/"+view.Rows[i].OrderNumber)
		}
		t.Errorf("no release naming %s in the ledger: %v", number, seen)
	}
	if !hold {
		t.Errorf("no hold naming %s in the ledger", number)
	}
}

func TestTheOrderPageShowsTheInvoiceChoice(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number := placeUnpaidOrder(t)

	var orderID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatalf("read the order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_preferences
		    (order_id, invoice_type, tax_id, customer_name, customer_email)
		VALUES ($1, 'company', '04595252', '測試股份有限公司',
		        'admin-invoice@goen.invalid')`, orderID); err != nil {
		t.Fatalf("record the preference: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if !view.HasInvoice() {
		t.Fatal("the order page does not show a 發票 preference that exists")
	}
	if view.InvoiceText(ctx) != "公司統編 04595252" {
		t.Errorf("the page says %q, want 公司統編 04595252", view.InvoiceText(ctx))
	}

	plain, err := s.Order(ctx, placeUnpaidOrder(t))
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if plain.HasInvoice() {
		t.Error("an order with no preference reports one")
	}
}

func TestAMistypedWarrantyTermIsRefusedNotDropped(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := draftProduct(t, ctx, s)
	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if errs, updateErr := s.UpdateProduct(ctx, &admin.ProductForm{
		Slug: slug, Name: view.Name, Summary: view.Summary, Description: view.Description,
		BrandID: view.BrandID, CategoryID: view.CategoryID, WarrantyMonths: 24,
	}); updateErr != nil || len(errs) > 0 {
		t.Fatalf("seed warranty term: %v %v", updateErr, errs)
	}

	form := url.Values{
		"name":            {view.Name},
		"summary":         {view.Summary},
		"description":     {view.Description},
		"brand":           {view.BrandID},
		"category":        {view.CategoryID},
		"warranty_months": {"12o"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/products/"+slug, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	res := httptest.NewRecorder()
	adminHandlerOver(pool, s).UpdateProduct(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		t.Errorf("UpdateProduct(warranty_months=%q) status = %d, want 422", "12o", res.Code)
	} else {
		body := res.Body.String()
		admintest.AssertRefusedInput(t, body, "p-warranty-months", "12o")
		admintest.AssertTextNumberControl(t, body, "p-warranty-months")
	}

	var months *int32
	if err := pool.QueryRow(ctx,
		`SELECT warranty_months FROM products WHERE slug = $1`, slug).Scan(&months); err != nil {
		t.Fatalf("read warranty after refusal: %v", err)
	}
	if months == nil || *months != 24 {
		got := "NULL"
		if months != nil {
			got = strconv.FormatInt(int64(*months), 10)
		}
		t.Errorf("warranty_months after mistyped edit = %s, want 24", got)
	}
}

func TestParseProtectedWritesDoNotCallInfrastructureARefusal(t *testing.T) {
	ctx, _ := staffContext(t)
	p := admintest.NamedPool(t, pool, "parse_closed_"+uuid.NewString()[:8])
	p.Close()
	s := admin.NewStore(p, fakeRefunder{}, nil, nil)

	writes := []struct {
		name  string
		write func() (map[string]string, error)
	}{
		{name: "create product", write: func() (map[string]string, error) {
			_, errs, err := s.CreateProduct(ctx, &admin.ProductForm{
				Slug: "closed-product", Name: "Closed", BrandID: uuid.NewString(), CategoryID: uuid.NewString(),
			})
			return errs, err
		}},
		{name: "update product", write: func() (map[string]string, error) {
			return s.UpdateProduct(ctx, &admin.ProductForm{
				Slug: "closed-product", Name: "Closed", BrandID: uuid.NewString(), CategoryID: uuid.NewString(),
			})
		}},
		{name: "add variant", write: func() (map[string]string, error) {
			return s.AddVariant(ctx, "closed-product", &admin.VariantForm{SKU: "CLOSED", PriceCents: 100})
		}},
		{name: "create method", write: func() (map[string]string, error) {
			return shipping.NewStore(p).CreateMethod(ctx, &shipping.NewMethod{
				Code: "closed_method", Destination: "address", Name: "Closed",
			})
		}},
	}
	for _, tt := range writes {
		t.Run(tt.name, func(t *testing.T) {
			errs, err := tt.write()
			if err == nil || errors.Is(err, admin.ErrRefused) || errors.Is(err, admin.ErrNotFound) {
				t.Errorf("write error category = %v, want infrastructure only", err)
			}
			if len(errs) != 0 {
				t.Errorf("write field errors = %v, want none for infrastructure", errs)
			}
		})
	}
}

func TestAMistypedParcelDimensionDoesNotBecomeUnmeasured(t *testing.T) {
	ctx, actor := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	h := adminHandlerOver(pool, s)
	slug := draftProduct(t, ctx, s)
	defer func() {
		if err := s.SetProductStatus(context.WithoutCancel(ctx), slug, "archived"); err != nil {
			t.Errorf("archive parcel fixture: %v", err)
		}
	}()

	tests := []struct {
		name  string
		field string
		id    string
		raw   string
	}{
		{name: "safety stock", field: "safety", id: "v-safety", raw: "1000001"},
		{name: "longest side", field: "parcel_longest", id: "v-longest", raw: "5001"},
		{name: "three-side sum", field: "parcel_sum", id: "v-sum", raw: "15001"},
		{name: "weight typo", field: "parcel_weight", id: "v-weight", raw: "10,000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sku := "PARSE-" + strings.ToUpper(uuid.NewString()[:8])
			form := url.Values{
				"sku":            {sku},
				"price":          {"1000"},
				"compare":        {""},
				"safety":         {"0"},
				"parcel_longest": {"450"},
				"parcel_sum":     {"1050"},
				"parcel_weight":  {"500"},
			}
			form.Set(tt.field, tt.raw)
			res := postVariantForm(t, h, ctx, slug, form)
			if res.Code != http.StatusUnprocessableEntity {
				t.Errorf("AddVariant(%s=%q) status = %d, want 422", tt.field, tt.raw, res.Code)
			} else {
				body := res.Body.String()
				admintest.AssertRefusedInput(t, body, tt.id, tt.raw)
				for id, field := range map[string]string{
					"v-sku": "sku", "v-price": "price", "v-compare": "compare",
					"v-safety": "safety", "v-longest": "parcel_longest",
					"v-sum": "parcel_sum", "v-weight": "parcel_weight",
				} {
					if got := admintest.InputAttribute(t, admintest.InputElementByID(t, body, id), "value"); got != form.Get(field) {
						t.Errorf("input %q value = %q, want submitted %q", id, got, form.Get(field))
					}
				}
				for _, id := range []string{"v-safety", "v-longest", "v-sum", "v-weight"} {
					admintest.AssertTextNumberControl(t, body, id)
				}
			}
			var count int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM product_variants WHERE sku = $1`, sku).Scan(&count); err != nil {
				t.Fatalf("count refused variant: %v", err)
			}
			if count != 0 {
				t.Errorf("AddVariant(%s=%q) inserted %d rows", tt.field, tt.raw, count)
			}
		})
	}

	measuredSKU := "MEASURED-" + strings.ToUpper(uuid.NewString()[:8])
	measured := url.Values{
		"sku": {measuredSKU}, "price": {"1000"}, "safety": {"0"},
		"parcel_longest": {"450"}, "parcel_sum": {"1050"}, "parcel_weight": {"15000"},
	}
	if res := postVariantForm(t, h, ctx, slug, measured); res.Code != http.StatusSeeOther {
		t.Fatalf("add measured variant status = %d, want 303", res.Code)
	}
	unmeasuredSKU := "UNMEASURED-" + strings.ToUpper(uuid.NewString()[:8])
	unmeasured := url.Values{
		"sku": {unmeasuredSKU}, "price": {"1000"}, "safety": {"0"},
		"parcel_longest": {""}, "parcel_sum": {""}, "parcel_weight": {""},
	}
	if res := postVariantForm(t, h, ctx, slug, unmeasured); res.Code != http.StatusSeeOther {
		t.Fatalf("add unmeasured variant status = %d, want 303", res.Code)
	}

	variantIDs := make(map[string]uuid.UUID, 2)
	rows, err := pool.Query(ctx, `
		SELECT sku, id FROM product_variants WHERE sku = ANY($1::text[])
		ORDER BY sku`, []string{measuredSKU, unmeasuredSKU})
	if err != nil {
		t.Fatalf("read parcel variants: %v", err)
	}
	for rows.Next() {
		var sku string
		var id uuid.UUID
		if err := rows.Scan(&sku, &id); err != nil {
			t.Fatalf("scan parcel variant: %v", err)
		}
		variantIDs[sku] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate parcel variants: %v", err)
	}
	rows.Close()
	if len(variantIDs) != 2 {
		t.Fatalf("read %d parcel variants, want 2", len(variantIDs))
	}
	for _, sku := range []string{measuredSKU, unmeasuredSKU} {
		if err := stock.NewStore(pool).Receive(ctx, sku, 2, actor.String(), "parse-stock-"+uuid.NewString()); err != nil {
			t.Fatalf("stock %s: %v", sku, err)
		}
	}
	if err := s.SetProductStatus(ctx, slug, "active"); err != nil {
		t.Fatalf("publish parcel product: %v", err)
	}

	methodCode := "parse_pickup_" + uuid.NewString()[:8]
	if errs, err := shipping.NewStore(pool).CreateMethod(ctx, &shipping.NewMethod{
		Code: methodCode, Destination: "pickup_point", Name: "量測超取",
		Carrier: "測試承運人", MaxParcelWeightG: 10000,
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateMethod: %v %v", err, errs)
	}
	var methodID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_methods WHERE code = $1`, methodCode).Scan(&methodID); err != nil {
		t.Fatalf("read parcel method: %v", err)
	}
	defer func() {
		if err := shipping.NewStore(pool).SetMethodActive(context.WithoutCancel(ctx), methodID.String(), false); err != nil {
			t.Errorf("deactivate parcel method: %v", err)
		}
	}()

	basket := cart.NewStore(pool)
	for _, tt := range []struct {
		name    string
		sku     string
		offered bool
	}{
		{name: "measured oversized parcel", sku: measuredSKU, offered: false},
		{name: "unmeasured parcel", sku: unmeasuredSKU, offered: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cartID, err := basket.Create(ctx, uuid.NewString(), uuid.NullUUID{})
			if err != nil {
				t.Fatalf("create cart: %v", err)
			}
			if addErr := basket.Add(ctx, cartID, variantIDs[tt.sku], 1); addErr != nil {
				t.Fatalf("add %s to cart: %v", tt.sku, addErr)
			}
			choices, err := basket.ShippingChoices(ctx, cartID, 100000)
			if err != nil {
				t.Fatalf("ShippingChoices: %v", err)
			}
			codes := make([]string, 0, len(choices))
			for i := range choices {
				codes = append(codes, choices[i].Code)
			}
			if got := slices.Contains(codes, methodCode); got != tt.offered {
				t.Errorf("ShippingChoices(%s) offered %q = %v, want %v; choices=%v",
					tt.sku, methodCode, got, tt.offered, codes)
			}
		})
	}
}

func TestAParcelSumCannotBeShorterThanItsLongestSide(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := draftProduct(t, ctx, s)
	sku := "SHORT-SUM-" + strings.ToUpper(uuid.NewString()[:8])
	form := url.Values{
		"sku": {sku}, "price": {"1000"}, "safety": {"0"},
		"parcel_longest": {"500"}, "parcel_sum": {"499"}, "parcel_weight": {"1000"},
	}
	res := postVariantForm(t, adminHandlerOver(pool, s), ctx, slug, form)
	if res.Code != http.StatusUnprocessableEntity {
		t.Errorf("AddVariant(parcel_sum < parcel_longest) status = %d, want 422", res.Code)
	} else {
		admintest.AssertRefusedInput(t, res.Body.String(), "v-sum", "499")
	}
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM product_variants WHERE sku = $1`, sku).Scan(&count); err != nil {
		t.Fatalf("count short-sum variant: %v", err)
	}
	if count != 0 {
		t.Errorf("AddVariant(parcel_sum < parcel_longest) inserted %d rows", count)
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO product_variants
			(product_id, sku, price_cents, safety_stock, parcel_longest_mm, parcel_sum_mm)
		SELECT id, $2, 100000, 0, 500, 499 FROM products WHERE slug = $1`,
		slug, "DIRECT-SHORT-"+strings.ToUpper(uuid.NewString()[:8]))
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok ||
		pgErr.ConstraintName != "product_variants_parcel_sum_covers_longest" {
		t.Errorf("direct short parcel sum error = %v, want constraint %q", err,
			"product_variants_parcel_sum_covers_longest")
	}
}

func TestTheShopSetsEachProductsWarrantyTerm(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := draftProduct(t, ctx, s)

	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if view.WarrantyMonths != 0 || view.WarrantyMonthsText() != "" {
		t.Errorf("a new product reports %d months (%q)",
			view.WarrantyMonths, view.WarrantyMonthsText())
	}

	form := &admin.ProductForm{
		Slug: slug, Name: view.Name, Summary: view.Summary,
		Description: view.Description, BrandID: view.BrandID,
		CategoryID: view.CategoryID, WarrantyMonths: 24,
	}
	if errs, updErr := s.UpdateProduct(ctx, form); updErr != nil || len(errs) > 0 {
		t.Fatalf("UpdateProduct: %v %v", updErr, errs)
	}

	after, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if after.WarrantyMonths != 24 {
		t.Errorf("the product states %d months, want 24", after.WarrantyMonths)
	}

	form.WarrantyMonths = 0
	if errs, updErr := s.UpdateProduct(ctx, form); updErr != nil || len(errs) > 0 {
		t.Fatalf("UpdateProduct: %v %v", updErr, errs)
	}
	var months *int32
	if scanErr := pool.QueryRow(ctx,
		`SELECT warranty_months FROM products WHERE slug = $1`, slug).Scan(&months); scanErr != nil {
		t.Fatalf("read the term: %v", scanErr)
	}
	if months != nil {
		t.Errorf("clearing the term left %d", *months)
	}

	form.WarrantyMonths = admin.MaxWarrantyMonths + 1
	errs, err := s.UpdateProduct(ctx, form)
	if err != nil {
		t.Fatalf("UpdateProduct: %v", err)
	}
	if errs["warranty_months"] == "" {
		t.Errorf("a %d-month term was accepted: %v", form.WarrantyMonths, errs)
	}
}

// returnedOrderWithStock is returnedOrder with a REAL variant behind its line, and its
// own product per call: returning produces stock, so a shared row fails shuffled.
func returnedOrderWithStock(t *testing.T, name string, qty int32) (requestID, variantID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	slug := name + "-" + uuid.NewString()[:8]
	var productID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, $1, '退貨測試商品', 'active', now()
		FROM brands b, categories c WHERE b.slug = 'pixelight' AND c.slug = 'phones'
		RETURNING id`, slug).Scan(&productID); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, safety_stock, position)
		VALUES ($1, upper($2), 100000, 0, 1) RETURNING id`,
		productID, slug).Scan(&variantID); err != nil {
		t.Fatalf("create variant: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT record_inventory_movement($1, 5, 'receipt', $2, 'admin', NULL, NULL)`,
		variantID, "seed:"+slug); err != nil {
		t.Fatalf("stock the variant: %v", err)
	}

	var orderID, lineID uuid.UUID
	var orderNumber string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, $2, upper($3), '退貨測試商品', 100000, 2) RETURNING id`,
		orderID, variantID, slug).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'r@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 200000)`,
		orderID, "cs_rets_"+orderNumber); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 200000, NULL, NULL)`,
		"cs_rets_"+orderNumber); err != nil {
		t.Fatalf("capture: %v", err)
	}
	moveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'black_cat', 'TS-'||$2) RETURNING id`,
		orderID, orderNumber).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 2)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason) VALUES ($1, '不合用') RETURNING id`,
		orderID).Scan(&requestID); err != nil {
		t.Fatalf("create return request: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, $4)`, orderID, requestID, lineID, qty); err != nil {
		t.Fatalf("create return line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return requestID, variantID
}

func stockOf(t *testing.T, variantID uuid.UUID) int32 {
	t.Helper()
	var n int32
	if err := pool.QueryRow(t.Context(),
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, variantID).Scan(&n); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return n
}

func returnLineID(t *testing.T, requestID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(),
		`SELECT order_line_id FROM return_request_lines WHERE return_request_id = $1`,
		requestID).Scan(&id); err != nil {
		t.Fatalf("read return line: %v", err)
	}
	return id
}

func TestAnInspectedReturnPutsTheSellableUnitsBack(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(restockAdminPool(t, pool), fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, variantID := returnedOrderWithStock(t, "restock", 2)
	lineID := returnLineID(t, requestID)

	before := stockOf(t, variantID)
	if err := s.Decide(ctx, requestID.String(), "approved", "", "", actor); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got := stockOf(t, variantID); got != before {
		t.Fatalf("approving a return moved stock %d -> %d; nothing has come back yet",
			before, got)
	}

	subscriptions := waitForRestock(t, variantID)
	if err := s.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: lineID, Received: 2, Restocked: 1, Note: "一件外盒破損",
	}}, actor); err != nil {
		t.Fatalf("inspect: %v", err)
	}

	assertRestockQueued(t, pool, subscriptions)
	if got, want := stockOf(t, variantID), before+1; got != want {
		t.Errorf("stock is %d after restocking one of two returned units, want %d", got, want)
	}

	var reason, sourceType string
	var delta int32
	if err := pool.QueryRow(ctx, `
		SELECT reason, coalesce(source_type, ''), delta FROM inventory_movements
		WHERE variant_id = $1 ORDER BY created_at DESC LIMIT 1`,
		variantID).Scan(&reason, &sourceType, &delta); err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	if reason != "return" || delta != 1 {
		t.Errorf("the ledger says %q %+d, want return +1", reason, delta)
	}
	if sourceType != "return_request" {
		t.Errorf("the movement points at %q, want return_request — a shop asking "+
			"why the number moved gets no answer otherwise", sourceType)
	}
}

func TestAReturnCannotCloseWithAnUninspectedLine(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, variantID := returnedOrderWithStock(t, "uninspected", 2)
	lineID := returnLineID(t, requestID)

	if err := s.Decide(ctx, requestID.String(), "approved", "", "", actor); err != nil {
		t.Fatalf("approve: %v", err)
	}

	if err := s.CompleteReturn(ctx, requestID.String(), "", actor); !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("closing an uninspected return = %v, want ErrRefused", err)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "approved" {
		t.Errorf("the return is %q after a refused completion, want approved", status)
	}

	if err := s.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: lineID, Received: 2, Restocked: 2,
	}}, actor); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if err := s.CompleteReturn(ctx, requestID.String(), "已退款並入庫", actor); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "completed" {
		t.Errorf("the return is %q after closing, want completed", status)
	}
	if got, want := stockOf(t, variantID), int32(5-0+2); got != want {
		t.Errorf("stock is %d after closing, want %d — completing a return must not "+
			"restock a second time", got, want)
	}
}

func TestInspectingIsRefusedBeforeApproval(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, variantID := returnedOrderWithStock(t, "unapproved", 2)
	lineID := returnLineID(t, requestID)

	before := stockOf(t, variantID)
	err := s.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: lineID, Received: 2, Restocked: 2,
	}}, actor)
	if !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("inspecting an undecided return = %v, want ErrRefused", err)
	}
	if got := stockOf(t, variantID); got != before {
		t.Errorf("stock moved %d -> %d on a refused inspection", before, got)
	}
}

func TestRestockingMoreThanArrivedIsRefused(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, variantID := returnedOrderWithStock(t, "overrestock", 1)
	lineID := returnLineID(t, requestID)

	if err := s.Decide(ctx, requestID.String(), "approved", "", "", actor); err != nil {
		t.Fatalf("approve: %v", err)
	}
	before := stockOf(t, variantID)

	err := s.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: lineID, Received: 1, Restocked: 2,
	}}, actor)
	if !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("restocking 2 of 1 received = %v, want ErrInvalid", err)
	}
	if got := stockOf(t, variantID); got != before {
		t.Errorf("stock moved %d -> %d on a refused inspection", before, got)
	}
}

func TestALineIsInspectedOnceAndACorrectionIsAnAdjustment(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, variantID := returnedOrderWithStock(t, "twice", 2)
	lineID := returnLineID(t, requestID)

	if err := s.Decide(ctx, requestID.String(), "approved", "", "", actor); err != nil {
		t.Fatalf("approve: %v", err)
	}
	before := stockOf(t, variantID)

	if err := s.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: lineID, Received: 2, Restocked: 2,
	}}, actor); err != nil {
		t.Fatalf("first inspection: %v", err)
	}

	err := s.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: lineID, Received: 2, Restocked: 1,
	}}, actor)
	if !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("re-inspecting = %v, want ErrRefused", err)
	}

	if got, want := stockOf(t, variantID), before+2; got != want {
		t.Errorf("stock is %d after a refused re-inspection, want %d", got, want)
	}
	var movements int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_movements
		WHERE variant_id = $1 AND reason = 'return'`, variantID).Scan(&movements); err != nil {
		t.Fatalf("count movements: %v", err)
	}
	if movements != 1 {
		t.Errorf("%d return movements after two inspections, want 1", movements)
	}

	var restocked int32
	if err := pool.QueryRow(ctx, `
		SELECT restocked_quantity FROM return_request_lines
		WHERE return_request_id = $1`, requestID).Scan(&restocked); err != nil {
		t.Fatalf("read the line: %v", err)
	}
	if restocked != 2 {
		t.Errorf("the line records %d restocked, want 2 — the refused submission "+
			"changed the record without changing the stock", restocked)
	}
}

func twoLineOrderWithStock(t *testing.T, name string) (
	number string, orderID uuid.UUID, lineIDs, variantIDs []uuid.UUID,
) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}

	for i := range 2 {
		slug := fmt.Sprintf("%s-%d-%s", name, i, uuid.NewString()[:8])
		var productID, variantID, lineID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
			SELECT b.id, c.id, $1, '分批出貨測試', 'active', now()
			FROM brands b, categories c WHERE b.slug = 'pixelight' AND c.slug = 'phones'
			RETURNING id`, slug).Scan(&productID); err != nil {
			t.Fatalf("create product: %v", err)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO product_variants (product_id, sku, price_cents, safety_stock, position)
			VALUES ($1, upper($2), 100000, 0, 1) RETURNING id`,
			productID, slug).Scan(&variantID); err != nil {
			t.Fatalf("create variant: %v", err)
		}
		if _, err := tx.Exec(ctx,
			`SELECT record_inventory_movement($1, 10, 'receipt', $2, 'admin', NULL, NULL)`,
			variantID, "seed:"+slug); err != nil {
			t.Fatalf("stock the variant: %v", err)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, $2, upper($3), '分批出貨測試', 100000, 3, $4) RETURNING id`,
			orderID, variantID, slug, i).Scan(&lineID); err != nil {
			t.Fatalf("create line: %v", err)
		}
		if _, err := tx.Exec(ctx,
			`SELECT hold_inventory($1, $2, 3, interval '30 minutes', $3)`,
			orderID, variantID, "hold:"+slug); err != nil {
			t.Fatalf("hold: %v", err)
		}
		lineIDs = append(lineIDs, lineID)
		variantIDs = append(variantIDs, variantID)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'p@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 600000)`,
		orderID, "cs_part_"+number); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 600000, NULL, NULL)`,
		"cs_part_"+number); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move to picking: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, orderID, lineIDs, variantIDs
}

func heldFor(t *testing.T, orderID, variantID uuid.UUID) int32 {
	t.Helper()
	var n int32
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce(sum(quantity), 0)::integer FROM inventory_reservations
		WHERE order_id = $1 AND variant_id = $2 AND state = 'held'`,
		orderID, variantID).Scan(&n); err != nil {
		t.Fatalf("read held: %v", err)
	}
	return n
}

func shippedFor(t *testing.T, lineID uuid.UUID) int32 {
	t.Helper()
	var n int32
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce(sum(quantity), 0)::integer FROM order_shipment_lines
		WHERE order_line_id = $1`, lineID).Scan(&n); err != nil {
		t.Fatalf("read shipped: %v", err)
	}
	return n
}

func TestAnOrderCanShipInTwoParcels(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, variants := twoLineOrderWithStock(t, "partial")

	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "black_cat", Tracking: "P1-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 2},
	}, actor); err != nil {
		t.Fatalf("first parcel: %v", err)
	}

	if got := shippedFor(t, lines[0]); got != 2 {
		t.Errorf("line 1 has shipped %d, want 2", got)
	}
	if got := shippedFor(t, lines[1]); got != 0 {
		t.Errorf("line 2 has shipped %d, want 0 — it was not in this parcel", got)
	}
	if got := heldFor(t, orderID, variants[0]); got != 1 {
		t.Errorf("line 1 still holds %d, want 1", got)
	}
	if got := heldFor(t, orderID, variants[1]); got != 3 {
		t.Errorf("line 2 still holds %d, want 3 — nothing of it has gone out", got)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "shipped" {
		t.Errorf("the order is %q after its first parcel, want shipped", status)
	}

	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "black_cat", Tracking: "P2-" + number,
	}, actor); err != nil {
		t.Fatalf("second parcel: %v", err)
	}

	if got := shippedFor(t, lines[0]); got != 3 {
		t.Errorf("line 1 has shipped %d after both parcels, want 3", got)
	}
	if got := shippedFor(t, lines[1]); got != 3 {
		t.Errorf("line 2 has shipped %d after both parcels, want 3", got)
	}
	if got := heldFor(t, orderID, variants[0]); got != 0 {
		t.Errorf("line 1 still holds %d after shipping everything, want 0 — a "+
			"sweeper would return goods that have gone out", got)
	}
	if got := heldFor(t, orderID, variants[1]); got != 0 {
		t.Errorf("line 2 still holds %d after shipping everything, want 0", got)
	}

	var parcels int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_shipments WHERE order_id = $1`, orderID).Scan(&parcels); err != nil {
		t.Fatalf("count parcels: %v", err)
	}
	if parcels != 2 {
		t.Errorf("%d parcels, want 2", parcels)
	}

	err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "black_cat", Tracking: "P3-" + number,
	}, actor)
	if !errors.Is(err, admin.ErrRefused) {
		t.Errorf("a third parcel on a fully shipped order = %v, want ErrRefused", err)
	}
}

func TestAParcelCannotCarryMoreThanRemains(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, variants := twoLineOrderWithStock(t, "overship")

	err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "black_cat", Tracking: "OVER-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 4},
	}, actor)
	if !errors.Is(err, admin.ErrQuantity) {
		t.Fatalf("shipping 4 of 3 = %v, want ErrQuantity", err)
	}

	var parcels int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_shipments WHERE order_id = $1`, orderID).Scan(&parcels); err != nil {
		t.Fatalf("count parcels: %v", err)
	}
	if parcels != 0 {
		t.Errorf("%d parcels after a refused dispatch, want 0", parcels)
	}
	if got := heldFor(t, orderID, variants[0]); got != 3 {
		t.Errorf("the hold moved to %d on a refused dispatch, want 3", got)
	}
}

func TestAnEmptyParcelIsRefused(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, _ := twoLineOrderWithStock(t, "emptyparcel")

	err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "black_cat", Tracking: "EMPTY-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 0, lines[1]: 0},
	}, actor)
	if !errors.Is(err, admin.ErrQuantity) {
		t.Fatalf("a parcel carrying nothing = %v, want ErrQuantity", err)
	}
	var parcels int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_shipments WHERE order_id = $1`, orderID).Scan(&parcels); err != nil {
		t.Fatalf("count parcels: %v", err)
	}
	if parcels != 0 {
		t.Errorf("%d parcels after an empty dispatch, want 0", parcels)
	}
}

func TestEachParcelTellsTheCustomer(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, _ := twoLineOrderWithStock(t, "notice")

	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "black_cat", Tracking: "N1-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 3},
	}, actor); err != nil {
		t.Fatalf("first parcel: %v", err)
	}
	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "black_cat", Tracking: "N2-" + number,
		Lines: map[uuid.UUID]int32{lines[1]: 3},
	}, actor); err != nil {
		t.Fatalf("second parcel: %v", err)
	}

	var notices int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = 'order.shipped' AND payload->>'order_number' = $1`,
		number).Scan(&notices); err != nil {
		t.Fatalf("count notices: %v", err)
	}
	if notices != 2 {
		t.Errorf("%d dispatch notices for two parcels, want 2 — a customer told "+
			"about one box is left wondering about the other", notices)
	}

	var events int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'shipped'`,
		orderID).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 2 {
		t.Errorf("%d shipped events for two parcels, want 2", events)
	}
}

func TestTheShopCanFindAWarrantyTheCustomerRegistered(t *testing.T) {
	ctx, _ := staffContext(t)
	serial, number := registeredWarranty(t, "SN-"+strings.ToUpper(uuid.NewString()[:8]))

	for _, term := range []struct {
		name string
		q    string
	}{
		{name: "by the serial off the label", q: serial},
		{name: "by the order number off the confirmation mail", q: number},
	} {
		t.Run(term.name, func(t *testing.T) {
			view, err := customers.NewStore(pool).Warranties(ctx, term.q)
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
		})
	}

	view, err := customers.NewStore(pool).Warranties(ctx, "SN-NOSUCHTHING")
	if err != nil {
		t.Fatalf("search warranties: %v", err)
	}
	if len(view.Rows) != 0 {
		t.Errorf("an unregistered serial found %d rows, want 0", len(view.Rows))
	}
}

func TestTheWarrantyLookupRefusesToListEverything(t *testing.T) {
	ctx, _ := staffContext(t)
	registeredWarranty(t, "SN-"+strings.ToUpper(uuid.NewString()[:8]))

	for _, term := range []string{"", " ", "A"} {
		view, err := customers.NewStore(pool).Warranties(ctx, term)
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

func registeredWarranty(t *testing.T, serial string) (registered, orderNumber string) {
	t.Helper()
	ctx := t.Context()

	var variantID, productID uuid.UUID
	var warrantyNote string
	var warrantyMonths int32
	if err := pool.QueryRow(ctx, `
		SELECT pv.id, p.id, coalesce(p.warranty_note, ''), p.warranty_months
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.is_active AND p.status = 'active' AND p.warranty_months IS NOT NULL
		ORDER BY pv.id LIMIT 1`).Scan(&variantID, &productID, &warrantyNote, &warrantyMonths); err != nil {
		t.Fatalf("find a variant of a product with a stated term: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('wr-' || gen_random_uuid() || '@goen.invalid', 'customer', '保固客戶')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create customer: %v", err)
	}

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}

	var lineID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (
			order_id, product_id, variant_id, sku, product_name,
			warranty_note, warranty_months, unit_price_cents, quantity
		)
		SELECT $1, $2, pv.id, pv.sku, p.name, nullif($4, ''), $5, 100000, 1
		FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $3 RETURNING order_lines.id`,
		orderID, productID, variantID, warrantyNote, warrantyMonths).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'wr@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	ref := "cs_warranty_" + number
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, ref); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`, ref); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	moveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number, shipped_at, delivered_at)
		VALUES ($1, 'black_cat', 'WR-' || $2, now() - interval '5 days', now() - interval '3 days')
		RETURNING id`, orderID, number).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := warranty.NewStore(pool).Register(
		ctx, lineID.String(), userID.String(), serial, 1); err != nil {
		t.Fatalf("register warranty: %v", err)
	}
	return serial, number
}

func TestCategoryIconVocabularyMatchesTheDatabaseConstraint(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var parentID uuid.UUID
	if queryErr := tx.QueryRow(ctx, `
		INSERT INTO categories (slug, name, position)
		VALUES ($1, '圖示字彙測試',
		        (SELECT coalesce(max(position), -1) + 1 FROM categories WHERE parent_id IS NULL))
		RETURNING id`, "icon-vocabulary-"+uuid.NewString()[:8]).Scan(&parentID); queryErr != nil {
		t.Fatalf("create parent: %v", queryErr)
	}
	for position, key := range icons.CategoryKeys() {
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO categories (parent_id, slug, name, icon_key, position)
			VALUES ($1, $2, $3, $3, $4)`,
			parentID, "icon-"+key+"-"+uuid.NewString()[:8], key, position); execErr != nil {
			t.Fatalf("database rejects renderer-owned icon %q: %v", key, execErr)
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO categories (parent_id, slug, name, icon_key, position)
		VALUES ($1, $2, '未知圖示', 'rocket', $3)`,
		parentID, "icon-unknown-"+uuid.NewString()[:8], len(icons.CategoryKeys()))
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "categories_icon_key_known" {
		t.Fatalf("unknown persisted icon was refused by %v, want categories_icon_key_known", err)
	}
}

// TestTheLoserOfTwoSimultaneousDecisionsPostsNoCredit is the same rule with no
// provider anywhere in it: a wholly credit-funded order has no payment row, so
// the losing decision's payout would be a single INSERT into the ledger, raising
// the customer's balance on a return the shop had just refused.
func TestTheLoserOfTwoSimultaneousDecisionsPostsNoCredit(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	// WHOLLY credit-funded: no payment row exists, so the payout on this path is
	// one INSERT into the ledger and there is no provider to blame.
	requestID, _, accountID := creditFundedReturn(t, 2, 200000)
	before := creditBalance(t, accountID)

	t1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin T1: %v", err)
	}
	defer func() { _ = t1.Rollback(ctx) }()
	if _, err := t1.Exec(ctx, `
		UPDATE return_requests SET status = 'rejected', decided_at = now()
		WHERE id = $1 AND status = 'requested'`, requestID); err != nil {
		t.Fatalf("T1 decide: %v", err)
	}

	decided := make(chan error, 1)
	go func() { decided <- s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}) }()

	select {
	case err := <-decided:
		t.Fatalf("T2 finished before T1 committed (%v); it never met the lock", err)
	case <-time.After(250 * time.Millisecond):
	}
	if err := t1.Commit(ctx); err != nil {
		t.Fatalf("commit T1: %v", err)
	}
	if err := <-decided; !errors.Is(err, admin.ErrRefused) {
		t.Errorf("the second decision returned %v, want ErrRefused", err)
	}

	if after := creditBalance(t, accountID); after != before {
		t.Errorf("store credit went %d -> %d on a return that was REJECTED", before, after)
	}
}

func creditBalance(t *testing.T, accountID uuid.UUID) int64 {
	t.Helper()
	var cents int64
	if err := pool.QueryRow(t.Context(),
		`SELECT coalesce(sum(amount_cents), 0)::bigint FROM store_credit_entries
		 WHERE account_id = $1`, accountID).Scan(&cents); err != nil {
		t.Fatalf("read credit balance: %v", err)
	}
	return cents
}

// TestAnOrderCannotFinishWhileItStillOwesAParcel keeps stock from being
// stranded permanently and invisibly.
//
// An order ships in as many parcels as it takes, and only the first moves the
// status. Finishing one that still owes a parcel would leave the remaining
// reservations `held` with no door out: release_reservation refuses them by name
// because a completed order is committed, ExpiredReservations excludes committed
// orders by predicate, and /admin/health counts expired holds with that same
// predicate.
//
// It refuses rather than releasing: what has not gone out is either still going
// out — CanShip already allows the second parcel — or it is an abandonment,
// which is a decision a person makes rather than a side effect of a dropdown.
func TestAnOrderCannotFinishWhileItStillOwesAParcel(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, variants := twoLineOrderWithStock(t, "unfinished")

	// One parcel, carrying part of the first line only.
	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "black_cat", Tracking: "U1-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 1},
	}, actor); err != nil {
		t.Fatalf("first parcel: %v", err)
	}

	// DELIVERED is allowed and must be: it is a fact about the parcel that went
	// out, and it is the ONLY thing that stamps order_shipments.delivered_at.
	// Refuse it and /admin/returns reads 尚未送達 for goods the customer holds,
	// on the screen built to inform a 消保法 §19 decision.
	if _, err := s.Advance(ctx, number, "delivered", actor); err != nil {
		t.Fatalf("a partially shipped order could not record its first parcel as "+
			"delivered: %v", err)
	}
	var stamped int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_shipments sh JOIN orders o ON o.id = sh.order_id
		WHERE o.order_number = $1 AND sh.delivered_at IS NOT NULL`,
		number).Scan(&stamped); err != nil {
		t.Fatalf("count stamped parcels: %v", err)
	}
	if stamped == 0 {
		t.Error("no parcel was stamped delivered, so the rescission window never starts")
	}

	// COMPLETED is refused: the order is not finished while it still owes a parcel.
	_, err := s.Advance(ctx, number, "completed", actor)
	if err == nil {
		t.Fatal("an order still owing a parcel was completed; whatever is still " +
			"held is now stranded with no door out")
	}
	// The `ok` is asserted, not used as a condition: wrapping that puts the
	// message in a string and the PgError nowhere in the chain would skip the
	// whole clause, leaving the test asking only that SOMETHING was refused.
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		t.Fatalf("the refusal does not carry the rule that made it: %v\n"+
			"Asserting only that an error happened cannot tell one rule from another", err)
	}
	if pgErr.ConstraintName != "orders_finished_when_shipped" {
		t.Errorf("refused by %q, want orders_finished_when_shipped — a statement "+
			"meant to prove one rule often trips another first", pgErr.ConstraintName)
	}

	// And once everything has gone out, finishing works and nothing is held.
	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "black_cat", Tracking: "U2-" + number,
	}, actor); err != nil {
		t.Fatalf("second parcel: %v", err)
	}
	if _, err := s.Advance(ctx, number, "completed", actor); err != nil {
		t.Fatalf("a fully shipped order could not be completed: %v", err)
	}
	for i, v := range variants {
		if got := heldFor(t, orderID, v); got != 0 {
			t.Errorf("line %d still holds %d on a completed order", i+1, got)
		}
	}
}

// TestTwoCarriersSharingATrackingNumberBothNotify holds the dispatch notice's
// dedupe key to the fact it deduplicates.
//
// order_shipments is unique on (carrier, tracking_number) — the schema's own
// statement that a tracking number identifies a parcel only alongside who is
// carrying it. Keyed on the tracking number ALONE, a second shipment with a
// colliding number from a different carrier meets ON CONFLICT DO NOTHING in the
// outbox: Ship succeeds, the parcel goes out, and the customer is never told.
// Two parcels of one ORDER carry two tracking numbers; two parcels of different
// orders may share one.
func TestTwoCarriersSharingATrackingNumberBothNotify(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}

	const shared = "SHARED-1234567890"
	first, _, firstLines, _ := twoLineOrderWithStock(t, "carrier-a")
	second, _, secondLines, _ := twoLineOrderWithStock(t, "carrier-b")

	if err := s.Ship(ctx, first, admin.Dispatch{
		Carrier: "black_cat", Tracking: shared,
		Lines: map[uuid.UUID]int32{firstLines[0]: 1},
	}, actor); err != nil {
		t.Fatalf("first carrier: %v", err)
	}
	if err := s.Ship(ctx, second, admin.Dispatch{
		Carrier: "hct", Tracking: shared,
		Lines: map[uuid.UUID]int32{secondLines[0]: 1},
	}, actor); err != nil {
		t.Fatalf("second carrier: %v — the database allows the pair, so the "+
			"application must too", err)
	}

	var notices int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = 'order.shipped' AND payload->>'tracking' = $1`, shared).Scan(&notices); err != nil {
		t.Fatalf("count dispatch notices: %v", err)
	}
	if notices != 2 {
		t.Errorf("%d dispatch notices for two parcels sharing a tracking number, "+
			"want 2 — one customer's parcel left and nothing told them", notices)
	}
}

// TestASplitReturnResumesTheHalfThatFailed holds the resume gate to BOTH
// sources.
//
// A return can be paid from card and credit — card first, credit last — and the
// two commit separately: the card through the provider, the credit as a ledger
// entry afterwards. A gate asking only whether the CARD half settled reports a
// return whose credit compensation did NOT as finished, and no other door posts
// that credit.
//
// It also has to resume only what is MISSING. Re-sending a settled card refund
// meets refunds_settled_is_history and re-posting the credit meets its
// idempotency key, so a retry that sent both could never finish the failed half.
//
// The half-paid state is CONSTRUCTED rather than raced into: what is under test
// is the resume, not how the state arose.
func TestASplitReturnResumesTheHalfThatFailed(t *testing.T) {
	ctx, _ := staffContext(t)
	requestID, orderNumber, accountID := creditFundedReturn(t, 2, 60000)

	// Approved, with the CARD half settled under the return's own request key —
	// which is what refundRequestKey produces and what makes a retry find the
	// same row — and no credit entry at all.
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests SET status = 'approved', decided_at = now(),
		       resolution = '退貨完成'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve the return: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, reason,
		                     return_request_id, status, provider_ref, succeeded_at)
		SELECT p.id, 'return:' || $1::text, 140000, '退貨完成', $1::uuid, 'succeeded',
		       're_constructed', now()
		FROM payments p JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $2 AND p.status = 'succeeded'`,
		requestID, orderNumber); err != nil {
		t.Fatalf("settle the card half: %v", err)
	}
	if got := cardRefunded(t, orderNumber); got != 140000 {
		t.Fatalf("the card half reads %d, want 140000 — the fixture did not build the "+
			"state under test", got)
	}
	before := creditBalance(t, accountID)

	// The retry must RESUME the credit half rather than refuse the whole return.
	sent := &atomic.Int64{}
	s := admin.NewStore(pool, fakeRefunder{sent: sent}, nil, nil)
	if err := s.Decide(ctx, requestID.String(), "approved", "退貨完成", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("the retry was refused (%v), so the credit half can never be paid "+
			"and the customer stays short", err)
	}
	if after := creditBalance(t, accountID); after <= before {
		t.Errorf("store credit went %d -> %d; the failed half was not resumed", before, after)
	}

	// And the card half is not sent TO STRIPE twice. The durable row proves the
	// outcome; this provider-side counter proves the retry made no remote call.
	if n := sent.Load(); n != 0 {
		t.Errorf("the retry sent %d refund(s) to the provider; the card half had "+
			"already landed and resuming means paying only what is MISSING", n)
	}
	if got := cardRefunded(t, orderNumber); got != 140000 {
		t.Errorf("the card half is now %d, want 140000 — the retry re-sent a refund "+
			"that had already landed", got)
	}

	// The order now holds a refund from BOTH sources, which is the only shape
	// that can tell the two definitions of "what has gone back" apart. The back
	// office displays this total, while the invoice claim independently uses the
	// same authoritative view under lock. A card-only definition would leave the
	// 統一發票 recording part of a sale that was reversed.
	withInvoices := admin.NewStore(
		pool, fakeRefunder{}, noDocuments{}, disabledInvoiceWriter{},
	)
	view, viewErr := withInvoices.Order(ctx, orderNumber)
	if viewErr != nil {
		t.Fatalf("read the order: %v", viewErr)
	}
	credited := creditBalance(t, accountID) - before
	if credited <= 0 {
		t.Fatal("no credit was returned, so this proves nothing about the sum")
	}
	if want := int64(140000) + credited; view.RefundedCents != want {
		t.Errorf("the back office reports %d refunded and %d has gone back (card %d + credit %d).\n"+
			"The display and the database-derived allowance use one fact, "+
			"and a 折讓 short of what was refunded over-reports the sale to the 財政部",
			view.RefundedCents, want, 140000, credited)
	}
}

func cardRefunded(t *testing.T, orderNumber string) int64 {
	t.Helper()
	var cents int64
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce(sum(r.amount_cents), 0) FROM refunds r
		JOIN payments p ON p.id = r.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND r.status = 'succeeded'`,
		orderNumber).Scan(&cents); err != nil {
		t.Fatalf("read refunds: %v", err)
	}
	return cents
}

// TestADeliveredOrderCanStillShipWhatItOwes is the exit from a trap that would
// otherwise have none. orders_legal_transition permits shipped -> delivered with
// a line still outstanding, deliberately — delivered says the parcels that WENT
// OUT have arrived — and orders_finished_when_shipped then refuses 'completed'.
// Without a dispatch form at 'delivered' the order is wedged for ever and the
// outstanding line's hold is stranded: release_reservation refuses a committed
// order, ExpiredReservations excludes it, and /admin/health counts neither.
func TestADeliveredOrderCanStillShipWhatItOwes(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, _, lines, _ := twoLineOrderWithStock(t, "wedged")

	// One parcel carrying part of the first line, then straight to delivered:
	// what went out has arrived, and the rest is still to come.
	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "black_cat", Tracking: "D1-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 1},
	}, actor); err != nil {
		t.Fatalf("first parcel: %v", err)
	}
	if _, err := s.Advance(ctx, number, "delivered", actor); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("read the order: %v", err)
	}
	if !view.CanShip {
		t.Fatal("a delivered order that still owes a parcel offers no dispatch form, " +
			"and completing it is refused — the order is wedged and its remaining " +
			"hold is stranded off the shelf where nothing counts it")
	}

	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "black_cat", Tracking: "D2-" + number,
	}, actor); err != nil {
		t.Fatalf("the second parcel of a delivered order was refused: %v", err)
	}
	if _, err := s.Advance(ctx, number, "completed", actor); err != nil {
		t.Errorf("finishing an order that owes nothing was refused: %v", err)
	}

	var held int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_reservations ir
		JOIN orders o ON o.id = ir.order_id
		WHERE o.order_number = $1 AND ir.state = 'held'`, number).Scan(&held); err != nil {
		t.Fatalf("read the holds: %v", err)
	}
	if held != 0 {
		t.Errorf("%d hold(s) still on the shelf after everything shipped", held)
	}
}

// noDocuments is the read side with no filed documents. The figure under test
// is what has been REFUNDED, which is a question about money and not about
// documents, so the documents are the part that can be empty.
type noDocuments struct{}

func (noDocuments) Documents(context.Context, string) ([]invoice.Document, error) {
	return nil, nil
}

type recordingInvoiceWriter struct {
	orders     []string
	operations []uuid.UUID
}

func (*recordingInvoiceWriter) Issue(context.Context, string) (invoice.Document, error) {
	return invoice.Document{}, invoice.ErrDisabled
}

func (*recordingInvoiceWriter) Void(context.Context, string, string) error {
	return invoice.ErrDisabled
}

func (w *recordingInvoiceWriter) FileAllowance(
	_ context.Context, orderNumber string, operationID uuid.UUID,
) (invoice.Document, error) {
	w.orders = append(w.orders, orderNumber)
	w.operations = append(w.operations, operationID)
	return invoice.Document{Kind: "allowance", Number: "2026080715227214"}, nil
}

func TestAllowanceHTTPDoesNotAcceptCallerControlledMoney(t *testing.T) {
	ctx, _ := staffContext(t)
	writer := &recordingInvoiceWriter{}
	store := admin.NewStore(pool, fakeRefunder{}, noDocuments{}, writer)
	handler := adminHandlerOver(pool, store)
	const number = "GO-260901-000001"

	tests := []struct {
		name   string
		amount string
	}{
		{name: "amount omitted"},
		{name: "stale forged amount ignored", amount: "999999999999"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callsBefore := len(writer.orders)
			operationID := uuid.New()
			form := url.Values{"operation_id": {operationID.String()}}
			if tt.amount != "" {
				form.Set("amount", tt.amount)
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost,
				"/admin/orders/"+number+"/invoice/allowance",
				strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("number", number)
			res := httptest.NewRecorder()
			handler.AllowInvoice(res, req)
			if res.Code != http.StatusSeeOther ||
				res.Header().Get("Location") != "/admin/orders/"+number+"?allowed=1" {
				t.Fatalf("Allowance HTTP = %d %q, want success redirect",
					res.Code, res.Header().Get("Location"))
			}
			if len(writer.orders) != callsBefore+1 || len(writer.operations) != callsBefore+1 {
				t.Fatalf("writer call counts = orders %d operations %d, want %d",
					len(writer.orders), len(writer.operations), callsBefore+1)
			}
			at := callsBefore
			if writer.orders[at] != number || writer.operations[at] != operationID {
				t.Fatalf("writer calls = orders %v operations %v, want %s/%s",
					writer.orders, writer.operations, number, operationID)
			}
		})
	}
}

type disabledInvoiceWriter struct{}

func (disabledInvoiceWriter) Issue(context.Context, string) (invoice.Document, error) {
	return invoice.Document{}, invoice.ErrDisabled
}

func (disabledInvoiceWriter) Void(context.Context, string, string) error {
	return invoice.ErrDisabled
}

func (disabledInvoiceWriter) FileAllowance(
	context.Context, string, uuid.UUID,
) (invoice.Document, error) {
	return invoice.Document{}, invoice.ErrDisabled
}

// TestASplitReturnResumesWhenTheCREDITHalfLanded is the mirror of its neighbour.
//
// refundCard answers (ref, RefundPending, nil) for a refund Stripe has accepted
// and not settled — no error — so payApprovedReturn goes on to post the credit.
// goen consumes no refund webhook, so that row stays pending for ever and
// pressing 同意 again is the only door. splitRefund must therefore exclude this
// return's own compensation from the credit side, or the retry reads the credit
// it just posted as credit already returned and refuses the whole payout.
func TestASplitReturnResumesWhenTheCREDITHalfLanded(t *testing.T) {
	ctx, _ := staffContext(t)
	requestID, orderNumber, accountID := creditFundedReturn(t, 2, 60000)

	// Approved, with the CREDIT half posted under the key the compensation uses
	// and the card half left PENDING — which is what a Stripe refund that has
	// been accepted and not settled looks like.
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests SET status = 'approved', decided_at = now(),
		       resolution = '退貨完成'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve the return: %v", err)
	}
	var userID, orderID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT user_id, id FROM orders WHERE order_number = $1`, orderNumber).
		Scan(&userID, &orderID); err != nil {
		t.Fatalf("read the order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		SELECT post_store_credit($1, $2::bigint, $3::text, $4, $5::text, NULL)`,
		userID, int64(60000), "退貨補償", orderID,
		"return-credit:"+requestID.String()); err != nil {
		t.Fatalf("post the credit half: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, reason,
		                     return_request_id, status)
		SELECT p.id, 'return:' || $1::text, 140000, '退貨完成', $1::uuid, 'pending'
		FROM payments p JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $2 AND p.status = 'succeeded'`,
		requestID, orderNumber); err != nil {
		t.Fatalf("open the card half: %v", err)
	}
	if got := cardRefunded(t, orderNumber); got != 0 {
		t.Fatalf("the card half reads %d settled, want 0 — the fixture did not "+
			"build the state under test", got)
	}
	before := creditBalance(t, accountID)

	sent := &atomic.Int64{}
	s := admin.NewStore(pool, fakeRefunder{sent: sent}, nil, nil)
	if err := s.Decide(ctx, requestID.String(), "approved", "退貨完成", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("the retry was refused (%v), so the CARD half can never be sent "+
			"and the customer stays short — goen consumes no refund webhook, so "+
			"pressing 同意 again is the only door", err)
	}
	if sent.Load() != 1 {
		t.Errorf("the provider was called %d time(s); the card half was the one "+
			"still owed", sent.Load())
	}
	if after := creditBalance(t, accountID); after != before {
		t.Errorf("store credit went %d -> %d; the credit half had already landed "+
			"and resuming means paying only what is MISSING", before, after)
	}
	var events int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events e
		WHERE e.return_request_id = $1 AND e.kind = 'refunded'`, requestID).Scan(&events); err != nil {
		t.Fatalf("count timeline events after the card half settled: %v", err)
	}
	if events != 1 {
		t.Errorf("%d refunded events after both sources settled, want 1", events)
	}
}

// TestATerminalCardRetrySurvivesErasureAfterCreditLanded proves the two source
// obligations are independent. Once the exact frozen credit half is posted,
// erasure may detach the account; a later known-failed card attempt must still
// get a successor without trying to post credit to an erased customer again.
func TestATerminalCardRetrySurvivesErasureAfterCreditLanded(t *testing.T) {
	ctx, staffID := staffContext(t)
	requestID, orderNumber, accountID := creditFundedReturn(t, 2, 60000)
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests SET status = 'approved', decided_at = now(),
		       resolution = 'erasure retry'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve split return: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT compensate_return_with_credit($1, 60000, $2)`, requestID, staffID); err != nil {
		t.Fatalf("post frozen credit half: %v", err)
	}
	before := creditBalance(t, accountID)

	failed := admin.NewStore(pool, fakeRefunder{state: admin.RefundFailed}, nil, nil)
	if err := failed.Decide(ctx, requestID.String(), "approved", "erasure retry", "", uuid.NullUUID{}); err == nil {
		t.Fatal("known-failed card attempt was reported as settled")
	}
	var customerID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT user_id FROM orders WHERE order_number = $1`, orderNumber).Scan(&customerID); err != nil {
		t.Fatalf("read return owner: %v", err)
	}
	if err := account.NewStore(pool).Erase(ctx, customerID.String()); err != nil {
		t.Fatalf("erase after exact credit posting: %v", err)
	}
	var detached bool
	if err := pool.QueryRow(ctx, `
		SELECT user_id IS NULL FROM orders WHERE order_number = $1`, orderNumber).
		Scan(&detached); err != nil {
		t.Fatalf("read erased order owner: %v", err)
	}
	if !detached {
		t.Fatal("erasure did not detach the order owner")
	}

	sent := &atomic.Int64{}
	healthy := admin.NewStore(pool, fakeRefunder{sent: sent}, nil, nil)
	if err := healthy.Decide(ctx, requestID.String(), "approved", "erasure retry", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("retry terminal card attempt after erasure: %v", err)
	}
	if sent.Load() != 1 {
		t.Errorf("provider calls after erasure = %d, want one successor", sent.Load())
	}
	if after := creditBalance(t, accountID); after != before {
		t.Errorf("erased account credit changed %d -> %d; exact credit was already posted",
			before, after)
	}
	var attempts, succeeded int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status = 'succeeded')
		FROM refunds WHERE return_request_id = $1`, requestID).Scan(&attempts, &succeeded); err != nil {
		t.Fatalf("read post-erasure attempts: %v", err)
	}
	if attempts != 2 || succeeded != 1 {
		t.Errorf("post-erasure attempts/succeeded = %d/%d, want 2/1", attempts, succeeded)
	}
}

// TestARefusedSystemIssueIsOnTheHealthPage: nobody watches an automatic issue,
// so ECPay refusing it stays in front of a person until a later issue exists. A
// staff claim's refusal was shown to the person who pressed the button and is
// not listed.
func TestARefusedSystemIssueIsOnTheHealthPage(t *testing.T) {
	ctx, actor := staffContext(t)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))

	refuse := func(number string, orderID uuid.UUID, kind string) {
		t.Helper()
		staffActor := uuid.NullUUID{UUID: actor, Valid: kind == "staff"}
		if _, err := pool.Exec(ctx, `
			INSERT INTO invoice_operations
			    (order_id, kind, provider_key, amount_cents, request_payload,
			     actor_user_id, actor_id_snapshot, actor_kind, request_id,
			     status, last_error, created_at)
			VALUES ($1, 'issue', replace($2, '-', ''), 100000, '{}', $3, $3, $4,
			        'refused:' || $2, 'rejected', 'issue_provider_rejected_2000006',
			        now() - interval '1 minute')`,
			orderID, number, staffActor, kind); err != nil {
			t.Fatalf("record a refused %s issue for %s: %v", kind, number, err)
		}
	}
	listed := func(number string) bool {
		t.Helper()
		view, err := health.NewStore(pool).WorkerHealth(ctx, worker)
		if err != nil {
			t.Fatalf("health: %v", err)
		}
		for _, c := range view.StrandedClaims {
			if c.OrderNumber == number {
				return true
			}
		}
		return false
	}

	systemNumber, systemOrder := paidPickingOrderForUser(t, cancelPointsCustomer(t), 100000)
	refuse(systemNumber, systemOrder, "system")
	staffNumber, staffOrder := paidPickingOrderForUser(t, cancelPointsCustomer(t), 100000)
	refuse(staffNumber, staffOrder, "staff")
	creditNumber, creditOrder, _ := paidUnshippedOrder(t, 0, 100000, false)
	refuse(creditNumber, creditOrder, "system")

	if !listed(systemNumber) {
		t.Error("a refused automatic issue is not on /admin/health; the paid order " +
			"goes uninvoiced with nothing to say so")
	}
	if !listed(creditNumber) {
		t.Error("a refused automatic issue on an order store credit paid in full is not " +
			"on /admin/health; the order is pending, and owes its invoice all the same")
	}
	if listed(staffNumber) {
		t.Error("a staff claim's refusal is on /admin/health; the person who pressed " +
			"the button already saw it")
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_operations
		    (order_id, kind, provider_key, amount_cents, request_payload,
		     actor_user_id, actor_id_snapshot, request_id)
		VALUES ($1, 'issue', replace($2, '-', '') || 'R1', 100000, '{}', $3, $3,
		        'retry:' || $2)`, systemOrder, systemNumber, actor); err != nil {
		t.Fatalf("issue %s again: %v", systemNumber, err)
	}
	if listed(systemNumber) {
		t.Error("the refused automatic issue is still listed after staff issued the order again")
	}
}

// TestAStrandedInvoiceClaimIsOnTheHealthPage holds the alarm and the only
// auditable recovery door for an aged ambiguous Allowance.
//
// A 折讓 claim is taken before ECPay is asked, because their allowance endpoint
// carries no idempotency field. A call that was not ANSWERED keeps its claim —
// right, because whether the document was filed is not knowable from here — and
// that leaves a row nothing else can resolve, the shape
// payment_webhook_events.unreconciled already has.
func TestAStrandedInvoiceClaimIsOnTheHealthPage(t *testing.T) {
	ctx, actor := staffContext(t)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))

	number, orderID, _, _ := twoLineOrderWithStock(t, "stranded")
	var invoiceID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
		VALUES ($1, 'invoice', 'GD-' || substr(replace(gen_random_uuid()::text,'-',''),1,8), 100000)
		RETURNING id`, orderID).Scan(&invoiceID); err != nil {
		t.Fatalf("file an invoice: %v", err)
	}

	// A claim taken JUST NOW is a call in flight, not a stuck one: the window is
	// the whole distinction, so the durable operation fixture has to sit on the
	// far side of it. A pending invoice_documents row is not a tax document and
	// is deliberately no longer a state the schema admits.
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_operations
		    (order_id,kind,target_document_id,provider_key,amount_cents,
		     request_payload,actor_user_id,actor_id_snapshot,request_id,
		     send_attempts,last_send_at,last_error,created_at,updated_at)
		SELECT $1,'allowance',d.id,d.number,50000,
		       jsonb_build_object(
		           'invoice_number',d.number,
		           'invoice_date',to_char(shop_day(d.issued_at), 'YYYY-MM-DD'),
		           'customer_name','王小明','email','stranded@goen.invalid',
		           'amount_cents',50000,
		           'lines',jsonb_build_array(jsonb_build_object(
		               'description','退貨折讓','quantity',1,
		               'unit_price_cents',50000,'amount_cents',50000))),
		       $3,$3,$4,1,now()-interval '1 hour','allowance_not_yet_visible',
		       now()-interval '1 hour',now()-interval '1 hour'
		FROM invoice_documents d WHERE d.id=$2`,
		orderID, invoiceID, actor, "invoice-stranded:"+number); err != nil {
		t.Fatalf("strand a claim: %v", err)
	}

	view, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if view.ClaimsSettled() {
		t.Fatal("a 折讓 claim the provider never answered is invisible on the one " +
			"page built to show what needs doing — the only sign is a button that " +
			"refuses, on one order, months later")
	}
	if view.AllHealthy() {
		t.Error("the page reads healthy with a claim nobody can settle outstanding")
	}
	var named, actionable bool
	var operation string
	for _, c := range view.StrandedClaims {
		if c.OrderNumber == number {
			named = true
			actionable = c.CanAuthorizeResend
			operation = c.Operation
		}
	}
	if !named {
		t.Errorf("the claim is counted and not named; an operator needs the order "+
			"to go and look at ECPay. Got %d claim(s).", len(view.StrandedClaims))
	}
	if !actionable {
		t.Fatal("an aged empty Allowance lookup has no explicit one-resend authorization door")
	}

	// Naming the operation is not confirmation. The typed conclusion is required
	// and an active worker lease makes even that conclusion ineligible.
	post := func(values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/admin/health/reconcile", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res := httptest.NewRecorder()
		healthHandler(pool).Reconcile(res, req)
		return res
	}
	omitted := post(url.Values{"invoice_operation": {operation}})
	if omitted.Code != http.StatusSeeOther ||
		omitted.Header().Get("Location") != "/admin/health?notflagged=1" {
		t.Fatalf("unconfirmed allowance form = %d %q, want refusal redirect",
			omitted.Code, omitted.Header().Get("Location"))
	}

	leaseOwner := uuid.New()
	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations
		SET lease_owner=$2, lease_until=now()+interval '1 minute'
		WHERE id=$1`, operation, leaseOwner); err != nil {
		t.Fatalf("hold a live worker lease: %v", err)
	}
	form := url.Values{
		"invoice_operation":  {operation},
		"invoice_resolution": {"confirmed_absent"},
	}
	leasing := post(form)
	if leasing.Code != http.StatusSeeOther ||
		leasing.Header().Get("Location") != "/admin/health?notflagged=1" {
		t.Fatalf("authorization during lease = %d %q, want refusal redirect",
			leasing.Code, leasing.Header().Get("Location"))
	}
	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations SET lease_owner=NULL, lease_until=NULL WHERE id=$1`,
		operation); err != nil {
		t.Fatalf("release worker lease: %v", err)
	}

	accepted := post(form)
	if accepted.Code != http.StatusSeeOther ||
		accepted.Header().Get("Location") != "/admin/health?invoicequeued=1" {
		t.Fatalf("confirmed allowance form = %d %q, want queued redirect",
			accepted.Code, accepted.Header().Get("Location"))
	}
	duplicate := post(form)
	if duplicate.Code != http.StatusSeeOther ||
		duplicate.Header().Get("Location") != "/admin/health?notflagged=1" {
		t.Fatalf("duplicate authorization = %d %q, want refusal redirect",
			duplicate.Code, duplicate.Header().Get("Location"))
	}

	var authorizations, audits int
	var auditActor uuid.UUID
	var auditRequest string
	if err := pool.QueryRow(ctx, `
		SELECT op.resend_authorizations,
		       (SELECT count(*)::integer FROM audit_events a
		        WHERE a.entity_id=op.id
		          AND a.action=$2),
		       coalesce((SELECT a.actor_id_snapshot FROM audit_events a
		                 WHERE a.entity_id=op.id
		                   AND a.action=$2
		                 ORDER BY a.occurred_at LIMIT 1),
		                '00000000-0000-0000-0000-000000000000'::uuid),
		       coalesce((SELECT a.request_id FROM audit_events a
		                 WHERE a.entity_id=op.id
		                   AND a.action=$2
		                 ORDER BY a.occurred_at LIMIT 1), '')
		FROM invoice_operations op
		WHERE op.id=$1`, operation, audit.ActionAuthorizeAllowanceResend).
		Scan(&authorizations, &audits, &auditActor, &auditRequest); err != nil {
		t.Fatalf("read authorization and audit: %v", err)
	}
	if authorizations != 1 || audits != 1 || auditActor != actor ||
		auditRequest != "req-"+actor.String()[:8] {
		t.Fatalf("authorization/audit = %d/%d actor %s request %q",
			authorizations, audits, auditActor, auditRequest)
	}
}

func TestAPickupOrderDispatchNoticeIsMarkedAsPickup(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number := pickupOrderForCorrection(t)
	var orderID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueShippedNotice(ctx, orderID, "綠界", "PICKUP-"+number); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	var pickup bool
	if err := pool.QueryRow(ctx, `
		SELECT (payload->>'pickup')::boolean FROM outbox_messages
		WHERE topic = 'order.shipped' AND payload->>'tracking' = $1`, "PICKUP-"+number).Scan(&pickup); err != nil {
		t.Fatalf("read notice: %v", err)
	}
	if !pickup {
		t.Error("a convenience-store order's dispatch notice is not marked as pickup, so it reads as a home delivery")
	}
}

type fixedInvoices []invoice.Document

func (f fixedInvoices) Documents(context.Context, string) ([]invoice.Document, error) { return f, nil }

func TestTheOrderPageShowsAnInvoiceAtTheTimeTheProviderIssuedIt(t *testing.T) {
	ctx := t.Context()
	number := placeUnpaidOrder(t)
	issued := time.Date(2026, 9, 30, 9, 30, 0, 0, time.UTC) // ECPay said "2026-09-30 17:30:00" Taipei time
	s := admin.NewStore(pool, fakeRefunder{}, fixedInvoices{{Kind: "invoice", Number: "AB12345678", IssuedAt: issued}}, disabledInvoiceWriter{})

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if len(view.InvoiceDocuments) != 1 || view.InvoiceDocuments[0].IssuedAt != "2026-09-30 17:30" {
		t.Errorf("invoice documents = %+v, want one issued at 2026-09-30 17:30", view.InvoiceDocuments)
	}
}

func TestOrderSearchLabelsAnUnpaidOrderAsAwaitingPayment(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number := placeUnpaidOrder(t)

	view, err := s.Orders(ctx, "", number)
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	if len(view.Orders) != 1 || view.Orders[0].Number != number {
		t.Fatalf("searching %s found %+v, want that order only", number, view.Orders)
	}
	if got, want := view.Orders[0].StatusText, i18n.T(ctx, i18n.KeyAdminStatusPending); got != want {
		t.Errorf("a searched unpaid order reads %q, want %q", got, want)
	}
}

func TestAdvanceRefusesTheStatusAnOrderAlreadyHas(t *testing.T) {
	ctx, staff := staffContext(t)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number, orderID, _ := paidUnshippedOrder(t, 500000, 0, false)

	if _, err := s.Advance(ctx, number, pages.FulfillmentPicking, actor); err != nil {
		t.Fatalf("first picking: %v", err)
	}
	if _, err := s.Advance(ctx, number, pages.FulfillmentPicking, actor); !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("a repeated picking gave %v, want ErrRefused", err)
	}
	var events, audits int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'picking'),
		       (SELECT count(*) FROM audit_events WHERE action = 'order.advance' AND entity_id = $1)`,
		orderID).Scan(&events, &audits); err != nil {
		t.Fatalf("count: %v", err)
	}
	if events != 1 || audits != 1 {
		t.Errorf("picking recorded %d events and %d audit rows, want 1 and 1", events, audits)
	}

	pending := placeUnpaidOrder(t)
	if _, err := s.Advance(ctx, pending, pages.FulfillmentPending, actor); !errors.Is(err, admin.ErrRefused) {
		t.Errorf("pending on a pending order gave %v, want ErrRefused", err)
	}
}

func TestTheStatusMenuOffersOnlyWhatTheDatabaseWillAccept(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	offers := func(v *adminpages.OrderView, status pages.FulfillmentStatus) bool {
		for _, n := range v.Next {
			if n.Value == status {
				return true
			}
		}
		return false
	}

	unpaid, err := s.Order(ctx, placeUnpaidOrder(t))
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if offers(&unpaid, pages.FulfillmentPicking) {
		t.Error("an unpaid order is offered picking, which orders_funded_to_leave_pending refuses")
	}
	if !unpaid.NextIsDestructive() {
		t.Error("an unpaid order's menu preselects cancelling")
	}

	number, _, lines, _ := twoLineOrderWithStock(t, "menu")
	if err = s.Ship(ctx, number, admin.Dispatch{
		Carrier: "black_cat", Tracking: "MENU-" + number, Lines: map[uuid.UUID]int32{lines[0]: 1},
	}, actor); err != nil {
		t.Fatalf("first parcel: %v", err)
	}
	partly, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if offers(&partly, pages.FulfillmentCompleted) {
		t.Error("an order still owing a parcel is offered completed, which orders_finished_when_shipped refuses")
	}
	if !offers(&partly, pages.FulfillmentDelivered) {
		t.Error("delivered must stay offered: it is the only way to record that the first parcel arrived")
	}
}

func TestARefusedStatusMoveNamesItsReason(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	h := adminHandlerOver(pool, s)
	post := func(number, status string) string {
		t.Helper()
		form := url.Values{"status": {status}}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+number+"/status", strings.NewReader(form.Encode()))
		req.SetPathValue("number", number)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		backOffice.RequireStaff(h.AdvanceOrder)(w, req)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("POST status=%s = %d, want 303", status, w.Code)
		}
		return w.Header().Get("Location")
	}

	unpaid := placeUnpaidOrder(t)
	if got, want := post(unpaid, "picking"), "/admin/orders/"+unpaid+"?unfunded=1"; got != want {
		t.Errorf("picking an unpaid order redirected to %q, want %q", got, want)
	}

	number, _, lines, _ := twoLineOrderWithStock(t, "reason")
	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "black_cat", Tracking: "RSN-" + number, Lines: map[uuid.UUID]int32{lines[0]: 1},
	}, uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("first parcel: %v", err)
	}
	if got, want := post(number, "completed"), "/admin/orders/"+number+"?owesparcel=1"; got != want {
		t.Errorf("completing an order that owes a parcel redirected to %q, want %q", got, want)
	}
}

func TestAnAuditEntryNamesItsOrderAndLinksIt(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number := shippableOrder(t, "zh-Hant")
	if err := s.Ship(ctx, number, admin.Dispatch{Carrier: "black_cat", Tracking: "AUD-" + number},
		uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("Ship: %v", err)
	}

	view, err := audit.NewStore(pool).Events(ctx)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	for _, e := range view.Rows {
		if e.Action == "order.ship" && e.Subject == number {
			if e.Href != "/admin/orders/"+number {
				t.Errorf("the shipment entry links %q, want /admin/orders/%s", e.Href, number)
			}
			return
		}
	}
	t.Errorf("no order.ship entry names %s among %d rows", number, len(view.Rows))
}

// TestASystemAuditRowIsReadAsTheSystem: a system row carries no user and no
// snapshot, which must neither break the trail nor read as an erased account.
func TestASystemAuditRowIsReadAsTheSystem(t *testing.T) {
	ctx, _ := staffContext(t)
	trigger := "evt_audit_" + uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events (actor_kind, action, entity_table, request_id)
		VALUES ('system', 'invoice.issue', 'invoice_documents', $1)`, trigger); err != nil {
		t.Fatalf("record a system audit row: %v", err)
	}

	view, err := audit.NewStore(pool).Events(ctx)
	if err != nil {
		t.Fatalf("Audit with a system row: %v", err)
	}
	for _, e := range view.Rows {
		if e.RequestID == trigger {
			if !e.System {
				t.Errorf("the system row reads as person %q", e.Actor)
			}
			return
		}
	}
	t.Errorf("no audit row carries %s among %d rows", trigger, len(view.Rows))
}

func TestARefusedDispatchKeepsWhatWasTyped(t *testing.T) {
	ctx, _ := staffContext(t)
	h := adminHandlerOver(pool, admin.NewStore(pool, fakeRefunder{}, nil, nil))
	number, _, lines, _ := twoLineOrderWithStock(t, "retype")

	form := url.Values{
		"carrier": {"black_cat"}, "tracking": {"9001-2345"}, "qty_" + lines[0].String(): {"99"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+number+"/ship", strings.NewReader(form.Encode()))
	req.SetPathValue("number", number)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	backOffice.RequireStaff(h.Ship)(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a quantity above what is outstanding answered %d, want 422", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`value="black_cat"`, `value="9001-2345"`, `value="99"`, `id="ship-qty-error"`, `aria-invalid="true"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the refused dispatch is missing %q", want)
		}
	}
}

func TestTheDashboardAndTheQueueTabsSplitPendingTheSameWay(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	tab := func(v adminpages.OrdersView, status adminpages.QueueFilter) int64 {
		for _, tb := range v.Tabs {
			if tb.Value == status {
				return tb.Count
			}
		}
		t.Fatalf("no %q tab", status)
		return 0
	}
	before, err := s.Orders(ctx, "", "")
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	dashBefore, err := s.Dashboard(ctx)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}

	unpaid := placeUnpaidOrder(t)
	funded, _, _ := paidUnshippedOrder(t, 500000, 0, false)

	after, err := s.Orders(ctx, "", "")
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	dashAfter, err := s.Dashboard(ctx)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if got := tab(after, adminpages.QueueAwaitingPayment) - tab(before, adminpages.QueueAwaitingPayment); got != 1 {
		t.Errorf("the awaiting-payment tab grew by %d, want 1: a funded order is not awaiting payment", got)
	}
	if got := tab(after, adminpages.QueueReady) - tab(before, adminpages.QueueReady); got != 1 {
		t.Errorf("the ready tab grew by %d, want 1", got)
	}
	if got := dashAfter.PendingOrders - dashBefore.PendingOrders; got != 1 {
		t.Errorf("the awaiting-payment tile grew by %d, want 1", got)
	}
	if got := dashAfter.ReadyOrders - dashBefore.ReadyOrders; got != 1 {
		t.Errorf("the ready tile grew by %d, want 1", got)
	}

	for status, want := range map[adminpages.QueueFilter]struct{ in, out string }{
		adminpages.QueueAwaitingPayment: {in: unpaid, out: funded},
		adminpages.QueueReady:           {in: funded, out: unpaid},
	} {
		view, err := s.Orders(ctx, status, "")
		if err != nil {
			t.Fatalf("Orders(%s): %v", status, err)
		}
		var sawIn, sawOut bool
		for _, o := range view.Orders {
			sawIn = sawIn || o.Number == want.in
			sawOut = sawOut || o.Number == want.out
		}
		if !sawIn || sawOut {
			t.Errorf("tab %q lists the expected order = %t and the other = %t, want true and false", status, sawIn, sawOut)
		}
	}
}

func TestTheOrderPageSaysHowItWasPaidAndWhatWasRefunded(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	number, orderID, _ := paidUnshippedOrder(t, 500000, 0, false)

	if _, err := pool.Exec(ctx,
		`UPDATE payments SET card_brand = 'visa', card_last4 = '4242' WHERE order_id = $1`, orderID); err != nil {
		t.Fatalf("record the card: %v", err)
	}
	var refundID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, reason, status, provider_ref, succeeded_at)
		SELECT p.id, 'order-page:' || ($1::uuid)::text, 120000, '顧客改變心意', 'succeeded',
		       're_order_page_' || ($1::uuid)::text, now()
		FROM payments p WHERE p.order_id = $1 AND p.status = 'succeeded'
		RETURNING id`, orderID).Scan(&refundID); err != nil {
		t.Fatalf("record a refund: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT record_audit_event($1, 'refund.succeeded', 'refunds', $2)`,
		staff, refundID); err != nil {
		t.Fatalf("record who: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if view.Payment.Card != "Visa •••• 4242" || view.Payment.Captured != pages.TWD(500000) || view.Payment.PaidAt == "" {
		t.Errorf("payment = %+v, want the card, the captured amount and a time", view.Payment)
	}
	if len(view.Refunds) != 1 {
		t.Fatalf("refunds = %+v, want one", view.Refunds)
	}
	r := view.Refunds[0]
	if r.Amount != pages.TWD(120000) || r.Reason != "顧客改變心意" || r.Staff == "" || r.At == "" {
		t.Errorf("refund = %+v, want its amount, time, reason and staff member", r)
	}
}
