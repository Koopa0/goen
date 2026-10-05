//go:build integration

package products_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/productlabel"
	"github.com/koopa0/goen/internal/user"
	"time"
)

func TestProductLabelRoundTripUsesAdminRoleAndAuditsAtomically(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, actor := admintest.StaffContext(t, owner)
	var id uuid.UUID
	var slug string
	if err := owner.QueryRow(ctx, `SELECT id, slug FROM products WHERE status='active' ORDER BY slug LIMIT 1`).Scan(&id, &slug); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(owner.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["role"] = "admin"
	writer, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	readerConfig := cfg.Copy()
	readerConfig.ConnConfig.RuntimeParams["role"] = "store"
	reader, err := pgxpool.NewWithConfig(ctx, readerConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reader.Close)
	for role, connection := range map[string]*pgxpool.Pool{"admin": writer, "store": reader} {
		var currentRole string
		if err = connection.QueryRow(ctx, `SELECT current_user`).Scan(&currentRole); err != nil || currentRole != role {
			t.Fatalf("role=%q, want %q: %v", currentRole, role, err)
		}
	}
	s := products.NewStore(writer)
	input := &productlabel.Input{Origin: " 台灣 ", OriginEn: "Taiwan", DomesticPartyName: "Maker", DomesticPartyPhone: "0912345678", DomesticPartyAddress: "Address", NetQuantity: "1.2", NetUnit: productlabel.Piece, MinAgeMonths: "0"}
	if err = s.SetProductLabel(ctx, slug, input); err != nil {
		t.Fatal(err)
	}
	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	if view.LabelInput.Origin != "台灣" || view.LabelInput.NetQuantity != "1.2" || view.LabelInput.MinAgeMonths != "0" {
		t.Errorf("stored facts=%+v, want trimmed origin, quantity 1.2 and age 0", view.LabelInput)
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		pdp, readErr := product.NewStore(reader, slog.New(slog.DiscardHandler)).Load(i18n.WithLocale(ctx, locale), slug, product.Selection{})
		if readErr != nil {
			t.Fatal(readErr)
		}
		want := "台灣"
		if locale == i18n.En {
			want = "Taiwan"
		}
		if pdp.LabelFacts.Origin != want || pdp.LabelFacts.NetQuantity != "1.2" || len(pdp.LabelRows(ctx)) != 6 {
			t.Errorf("%s public facts=%+v, want origin %q, quantity 1.2 and six facts", locale, pdp.LabelFacts, want)
		}
	}
	var audits int
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='product.label.set' AND entity_id=$1 AND actor_user_id=$2 AND before->'origin'='null'::jsonb AND after->>'origin'='台灣' AND after->>'net_quantity'='1.20' AND after->>'min_age_months'='0'`, id, actor).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("matching audit rows=%d: %v", audits, err)
	}
	if err = s.SetProductLabel(t.Context(), slug, &productlabel.Input{Origin: "unaudited"}); !errors.Is(err, audit.ErrNoActor) {
		t.Fatalf("missing actor=%v", err)
	}
	view, err = s.Product(ctx, slug)
	if err != nil || view.LabelInput.Origin != "台灣" {
		t.Fatalf("audit failure changed facts=%+v: %v", view, err)
	}
	if err = s.SetProductLabel(ctx, "missing-label-product", input); !errors.Is(err, products.ErrNotFound) {
		t.Fatalf("missing product=%v", err)
	}
	if err = s.SetProductLabel(ctx, slug, &productlabel.Input{MinAgeMonths: "217"}); !errors.Is(err, products.ErrInvalid) {
		t.Fatalf("invalid input=%v", err)
	}
	if err = s.SetProductLabel(ctx, slug, &productlabel.Input{}); err != nil {
		t.Fatal(err)
	}
	pdp, err := product.NewStore(reader, slog.New(slog.DiscardHandler)).Load(ctx, slug, product.Selection{})
	if err != nil || len(pdp.LabelRows(ctx)) != 0 {
		t.Fatalf("cleared public facts=%+v: %v", pdp.LabelFacts, err)
	}
	var allNull bool
	if err = owner.QueryRow(ctx, `SELECT origin IS NULL AND origin_en IS NULL AND domestic_party_name IS NULL AND domestic_party_phone IS NULL AND domestic_party_address IS NULL AND net_quantity IS NULL AND net_unit IS NULL AND min_age_months IS NULL FROM products WHERE id=$1`, id).Scan(&allNull); err != nil || !allNull {
		t.Fatalf("clear fields all null=%v: %v", allNull, err)
	}
	if err = s.SetProductLabel(ctx, slug, &productlabel.Input{OriginEn: "Taiwan"}); err != nil {
		t.Fatal(err)
	}
	pdp, err = product.NewStore(reader, slog.New(slog.DiscardHandler)).Load(i18n.WithLocale(ctx, i18n.ZhHant), slug, product.Selection{})
	if err != nil || pdp.LabelFacts.Origin != "Taiwan" {
		t.Fatalf("English-only origin hidden=%+v: %v", pdp.LabelFacts, err)
	}
}

func TestProductLabelRoutesRefuseCustomersAndKeepInvalidForm(t *testing.T) {
	p := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, p)
	var slug string
	if err := p.QueryRow(ctx, `SELECT slug FROM products ORDER BY slug LIMIT 1`).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	s := products.NewStore(p)
	h := admintest.ProductDesk(p, s)
	mux := http.NewServeMux()
	h.Routes(mux, access.New(slog.New(slog.DiscardHandler), nil))
	path := "/admin/products/" + slug + "/label"
	for _, customer := range []bool{false, true} {
		requestCtx := t.Context()
		if customer {
			requestCtx = user.NewContext(requestCtx, user.User{ID: uuid.NewString(), Role: "customer"})
		}
		req := httptest.NewRequestWithContext(requestCtx, http.MethodPost, path, strings.NewReader("origin=forbidden"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		if response.Code != http.StatusNotFound {
			t.Fatalf("customer=%v status=%d", customer, response.Code)
		}
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		form := url.Values{"origin": {"<origin>"}, "net_quantity": {"1.001"}, "net_unit": {"oz"}, "min_age_months": {"217"}}
		req := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `value="&lt;origin&gt;"`) || !strings.Contains(response.Body.String(), `value="oz" selected`) || !strings.Contains(response.Body.String(), `value="217"`) {
			t.Fatalf("%s refused form status=%d lost input", locale, response.Code)
		}
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader("origin=Taiwan"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/products/"+slug+"?ok=1#sec-label" {
		t.Fatalf("saved redirect=%d %q", response.Code, response.Header().Get("Location"))
	}
	var audited int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='product.label.set'`).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("refused forms wrote audit rows=%d: %v", audited, err)
	}
}

func TestProductLabelRedirectSurvivesAProductReadFailure(t *testing.T) {
	p := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, p)
	var slug string
	if err := p.QueryRow(ctx, `SELECT slug FROM products ORDER BY slug LIMIT 1`).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	config := p.Config().Copy()
	config.ConnConfig.RuntimeParams["role"] = "admin"
	config.ConnConfig.Tracer = refuseFullProductRead{}
	writer, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	var role string
	if err = writer.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "admin" {
		t.Fatalf("label writer role=%q: %v", role, err)
	}
	handler := admintest.ProductDesk(p, products.NewStore(writer))
	mux := http.NewServeMux()
	handler.Routes(mux, access.New(slog.New(slog.DiscardHandler), nil))
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/products/"+slug+"/label", strings.NewReader("origin=Redirect+fixture"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/products/"+slug+"?ok=1#sec-label" {
		t.Fatalf("committed label redirect=%d %q, want 303 to the label section", response.Code, response.Header().Get("Location"))
	}
	var origin string
	if err = p.QueryRow(ctx, `SELECT origin FROM products WHERE slug=$1`, slug).Scan(&origin); err != nil || origin != "Redirect fixture" {
		t.Fatalf("committed label origin=%q: %v", origin, err)
	}
}

type refuseFullProductRead struct{}

func (refuseFullProductRead) TraceQueryStart(ctx context.Context, _ *pgx.Conn, query pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(query.SQL, "-- name: AdminProduct :one") {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		return canceled
	}
	return ctx
}

func (refuseFullProductRead) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestProductLabelDatabaseRefusalsKeepTheDraftAndLeaveNoWrite(t *testing.T) {
	for _, named := range []bool{true, false} {
		t.Run(fmt.Sprint(named), func(t *testing.T) {
			// This test tightens a CHECK, so it needs a database of its own.
			owner := admintest.Pool(t)
			ctx, _ := admintest.StaffContext(t, owner)
			var slug string
			if err := owner.QueryRow(ctx, `SELECT slug FROM products ORDER BY slug LIMIT 1`).Scan(&slug); err != nil {
				t.Fatal(err)
			}
			constraint := "products_label_future_rule"
			if named {
				constraint = "products_label_origin_valid"
			}
			if _, err := owner.Exec(ctx, `ALTER TABLE products DROP CONSTRAINT products_label_origin_valid`); err != nil {
				t.Fatal(err)
			}
			if _, err := owner.Exec(ctx, `ALTER TABLE products ADD CONSTRAINT `+constraint+` CHECK (origin IS NULL OR char_length(origin) <= 4)`); err != nil {
				t.Fatal(err)
			}
			cfg := owner.Config().Copy()
			cfg.ConnConfig.RuntimeParams["role"] = "admin"
			writer, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(writer.Close)
			var role string
			if err := writer.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "admin" {
				t.Fatalf("writer role=%q: %v", role, err)
			}
			mux := http.NewServeMux()
			admintest.ProductDesk(owner, products.NewStore(writer)).Routes(mux, access.New(slog.New(slog.DiscardHandler), nil))
			for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
				req := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodPost, "/admin/products/"+slug+"/label", strings.NewReader("origin=Taiwan&domestic_party_name=Retained+maker"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, req)
				body := response.Body.String()
				if response.Code != http.StatusUnprocessableEntity || response.Header().Get("Location") != "" || !strings.Contains(body, `value="Taiwan"`) || !strings.Contains(body, `value="Retained maker"`) {
					t.Fatalf("%s named=%v: refused status=%d lost draft", locale, named, response.Code)
				}
				if named {
					input := admintest.InputElementByID(t, body, "label-origin")
					if !strings.Contains(input, `aria-invalid="true"`) || !strings.Contains(input, `aria-describedby="label-origin-error"`) || !strings.Contains(body, i18n.T(i18n.WithLocale(ctx, locale), i18n.KeyProductLabelTextInvalid)) {
						t.Fatalf("%s: missing origin refusal: %s", locale, input)
					}
				} else if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, i18n.T(i18n.WithLocale(ctx, locale), i18n.KeyProductLabelRefused)) {
					t.Fatalf("%s: missing form refusal", locale)
				}
			}
			var unchanged bool
			if err := owner.QueryRow(ctx, `SELECT origin IS NULL AND domestic_party_name IS NULL FROM products WHERE slug=$1`, slug).Scan(&unchanged); err != nil || !unchanged {
				t.Fatalf("refused facts changed: unchanged=%v: %v", unchanged, err)
			}
			if n := admintest.AuditRows(t, owner, audit.ActionSetProductLabel); n != 0 {
				t.Fatalf("refused facts wrote %d audit rows", n)
			}
		})
	}
}

func TestProductLabelWritesDoNotBlockProductReferences(t *testing.T) {
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
	if _, err := db.New(tx).LockProductLabel(ctx, slug); err != nil {
		t.Fatal(err)
	}
	referenceCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := owner.Exec(referenceCtx, `INSERT INTO wishlist_items (user_id, product_id) SELECT $1, id FROM products WHERE slug=$2`, actor, slug); err != nil {
		t.Fatalf("label audit lock blocked a product reference: %v", err)
	}
}
