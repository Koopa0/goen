//go:build integration

package products_test

import (
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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/user"
)

func TestProductInvoiceLineFactsUseAdminRoleAndSnapshotOnOrderLines(t *testing.T) {
	p := admintest.Pool(t)
	ctx, actor := admintest.StaffContext(t, p)
	var id, variant uuid.UUID
	var slug, sku, name string
	var price int64
	if err := p.QueryRow(ctx, `SELECT p.id,p.slug,p.name,pv.id,pv.sku,pv.price_cents FROM products p JOIN product_variants pv ON pv.product_id=p.id ORDER BY p.slug,pv.id LIMIT 1`).Scan(&id, &slug, &name, &variant, &sku, &price); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(p.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["role"] = "admin"
	writer, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	storeConfig := cfg.Copy()
	storeConfig.ConnConfig.RuntimeParams["role"] = "store"
	customer, err := pgxpool.NewWithConfig(ctx, storeConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(customer.Close)
	for role, connection := range map[string]*pgxpool.Pool{"admin": writer, "store": customer} {
		var got string
		if err = connection.QueryRow(ctx, `SELECT current_user`).Scan(&got); err != nil || got != role {
			t.Fatalf("role=%q want %q: %v", got, role, err)
		}
	}
	s := products.NewStore(writer)
	facts := invoice.LineTerms{TaxType: invoice.Exempt, Unit: "包"}
	if err = s.SetProductInvoiceLine(ctx, slug, facts); err != nil {
		t.Fatal(err)
	}
	view, err := s.Product(ctx, slug)
	if err != nil || view.InvoiceTerms == nil || *view.InvoiceTerms != facts {
		t.Fatalf("saved facts=%+v: %v", view, err)
	}
	var audits int
	if err = p.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='product.invoice_line.set' AND entity_id=$1 AND actor_user_id=$2 AND before->>'tax_type'='taxable' AND before->>'invoice_unit'='個' AND after->>'tax_type'='exempt' AND after->>'invoice_unit'='包'`, id, actor).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("matching audit rows=%d: %v", audits, err)
	}
	if err = s.SetProductInvoiceLine(t.Context(), slug, invoice.LineTerms{TaxType: invoice.Taxable, Unit: invoice.DefaultUnit}); !errors.Is(err, audit.ErrNoActor) {
		t.Fatalf("missing actor=%v", err)
	}
	if err = s.SetProductInvoiceLine(ctx, slug, invoice.LineTerms{TaxType: invoice.ZeroRated, Unit: invoice.DefaultUnit}); !errors.Is(err, products.ErrInvalid) {
		t.Fatalf("zero-rated product=%v", err)
	}
	view, err = s.Product(ctx, slug)
	if err != nil || *view.InvoiceTerms != facts {
		t.Fatalf("refused writes changed facts=%+v: %v", view, err)
	}
	for _, column := range []string{"tax_type", "invoice_unit"} {
		_, writeErr := customer.Exec(ctx, `INSERT INTO order_lines (order_id,sku,product_name,unit_price_cents,quantity,`+column+`) VALUES ($1,'SNAPSHOT','Snapshot',100,1,'forged')`, uuid.New())
		pgErr, ok := errors.AsType[*pgconn.PgError](writeErr)
		if !ok || pgErr.Code != "42501" {
			t.Fatalf("store insert %s=%v, want column privilege refusal", column, writeErr)
		}
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pgtx.Rollback(ctx, tx)
	var order, line uuid.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO orders (order_number,shipping_version_id,shipping_method_code,shipping_method_name) SELECT next_order_number(),v.id,sm.code,v.name FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id=v.method_id ORDER BY v.effective_at LIMIT 1 RETURNING id`).Scan(&order); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO order_lines (order_id,variant_id,sku,product_name,unit_price_cents,quantity) VALUES ($1,$2,$3,$4,$5,1) RETURNING id`, order, variant, sku, name, price).Scan(&line); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO order_private_data (order_id,email,recipient_name,phone,postal_code,city,district,street) VALUES ($1,'invoice-facts@goen.invalid','Fixture','0912345678','110','台北市','信義區','路 1 號')`, order); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.SetProductInvoiceLine(ctx, slug, invoice.LineTerms{TaxType: invoice.Taxable, Unit: invoice.DefaultUnit}); err != nil {
		t.Fatal(err)
	}
	var tax, unit string
	if err = p.QueryRow(ctx, `SELECT tax_type,invoice_unit FROM order_lines WHERE id=$1`, line).Scan(&tax, &unit); err != nil || tax != "exempt" || unit != "包" {
		t.Fatalf("order snapshot tax=%q unit=%q: %v", tax, unit, err)
	}
}

func TestProductInvoiceLineRouteRefusesCustomersAndKeepsInvalidForm(t *testing.T) {
	p := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, p)
	var slug string
	if err := p.QueryRow(ctx, `SELECT slug FROM products ORDER BY slug LIMIT 1`).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	admintest.ProductDesk(p, products.NewStore(p)).Routes(mux, access.New(slog.New(slog.DiscardHandler), nil))
	path := "/admin/products/" + slug + "/invoice-line"
	for _, role := range []user.Role{"", "customer"} {
		requestCtx := t.Context()
		if role != "" {
			requestCtx = user.NewContext(requestCtx, user.User{ID: uuid.NewString(), Role: role})
		}
		req := httptest.NewRequestWithContext(requestCtx, http.MethodPost, path, strings.NewReader("tax_type=exempt&invoice_unit=x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		if response.Code != http.StatusNotFound {
			t.Fatalf("role=%q status=%d", role, response.Code)
		}
	}
	form := url.Values{"tax_type": {"zero_rated"}, "invoice_unit": {"<unit>"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `value="zero_rated" selected`) || !strings.Contains(response.Body.String(), `value="&lt;unit&gt;"`) {
		t.Fatalf("refused form status=%d lost values", response.Code)
	}
	var n int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='product.invoice_line.set'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("refused form audit rows=%d: %v", n, err)
	}
}

func TestProductInvoiceLineRedirectSurvivesAProductReadFailure(t *testing.T) {
	p := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, p)
	var slug string
	if err := p.QueryRow(ctx, `SELECT slug FROM products ORDER BY slug LIMIT 1`).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	config := p.Config().Copy()
	config.ConnConfig.RuntimeParams["role"] = "admin"
	config.ConnConfig.Tracer = refuseInvoiceFormProductRead{}
	writer, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	var role string
	if err = writer.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "admin" {
		t.Fatalf("invoice writer role=%q: %v", role, err)
	}
	handler := admintest.ProductDesk(p, products.NewStore(writer))
	mux := http.NewServeMux()
	handler.Routes(mux, access.New(slog.New(slog.DiscardHandler), nil))
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/products/"+slug+"/invoice-line", strings.NewReader("tax_type=exempt&invoice_unit=%E5%8C%85"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/products/"+slug+"?ok=1#sec-invoice" {
		t.Fatalf("committed invoice redirect=%d %q, want 303 to the invoice section", response.Code, response.Header().Get("Location"))
	}
	var taxType, unit string
	if err = p.QueryRow(ctx, `SELECT tax_type, invoice_unit FROM products WHERE slug=$1`, slug).Scan(&taxType, &unit); err != nil || taxType != "exempt" || unit != "包" {
		t.Fatalf("committed invoice terms=%q/%q: %v", taxType, unit, err)
	}
}

type refuseInvoiceFormProductRead struct{}

func (refuseInvoiceFormProductRead) TraceQueryStart(ctx context.Context, _ *pgx.Conn, query pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(query.SQL, "-- name: AdminProduct :one") {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		return canceled
	}
	return ctx
}

func (refuseInvoiceFormProductRead) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {
}

func TestProductInvoiceLineWritesDoNotBlockProductReferences(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, actor := admintest.StaffContext(t, owner)
	var slug string
	if err := owner.QueryRow(ctx, `SELECT slug FROM products ORDER BY slug LIMIT 1`).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	cfg := owner.Config().Copy()
	cfg.ConnConfig.RuntimeParams["role"] = "admin"
	writer, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	tx, err := writer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pgtx.Rollback(ctx, tx)
	if _, err := db.New(tx).LockProductInvoiceLine(ctx, slug); err != nil {
		t.Fatal(err)
	}
	referenceCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := owner.Exec(referenceCtx, `INSERT INTO wishlist_items (user_id, product_id) SELECT $1, id FROM products WHERE slug=$2`, actor, slug); err != nil {
		t.Fatalf("invoice audit lock blocked a product reference: %v", err)
	}
}
