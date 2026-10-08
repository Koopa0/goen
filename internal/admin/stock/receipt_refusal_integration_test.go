//go:build integration

package stock_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/i18n"
)

func TestReceiptRefusalPreservesDraftAndRetryThroughRegisteredRoutes(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	staff := admintest.AdminRolePool(t, pool)
	var role string
	if err := staff.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "admin" {
		t.Fatalf("receipt writer role = %q: %v", role, err)
	}
	mux := http.NewServeMux()
	handlerOver(stock.NewStore(staff)).Routes(mux, admintest.BackOffice)

	for _, locale := range i18n.Locales() {
		for _, tt := range []struct {
			name     string
			raw      string
			noKey    bool
			errorKey i18n.Key
		}{
			{name: "above the bound", raw: "10001", errorKey: i18n.KeyAdminNoticeBadQty},
			{name: "negative", raw: "-1", errorKey: i18n.KeyAdminNoticeBadQty},
			{name: "fraction", raw: "1.5", errorKey: i18n.KeyAdminNoticeBadQty},
			{name: "malformed", raw: ` 12<x> `, errorKey: i18n.KeyAdminNoticeBadQty},
			{name: "empty without key", noKey: true, errorKey: i18n.KeyAdminNoticeBadQty},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				requestCtx := i18n.WithLocale(ctx, locale)
				sku, variant := receiptVariant(t, pool)
				get := httptest.NewRecorder()
				mux.ServeHTTP(get, httptest.NewRequestWithContext(requestCtx, http.MethodGet, "/admin/stock/"+sku, nil))
				if get.Code != http.StatusOK {
					t.Fatalf("receipt page = %d, want 200", get.Code)
				}
				key := receiptKey(t, get.Body.String())
				before := receiptState(t, pool, variant)
				form := url.Values{"sku": {sku}, "quantity": {tt.raw}}
				if !tt.noKey {
					form.Set("idempotency", key)
				}
				refused := postReceipt(requestCtx, mux, form)
				if refused.Code != http.StatusUnprocessableEntity || refused.Header().Get("Location") != "" {
					t.Fatalf("refused receipt = %d %q, want 422 without redirect", refused.Code, refused.Header().Get("Location"))
				}
				body := refused.Body.String()
				admintest.AssertRefusedInput(t, body, "receive-qty", tt.raw)
				admintest.AssertTextNumberControl(t, body, "receive-qty")
				if !strings.Contains(body, i18n.T(requestCtx, tt.errorKey)) {
					t.Errorf("receipt refusal lacks localized reason %q", i18n.T(requestCtx, tt.errorKey))
				}
				retained := receiptKey(t, body)
				if !tt.noKey && retained != key {
					t.Errorf("refused receipt key = %q, want submitted key %q", retained, key)
				}
				if diff := cmp.Diff(before, receiptState(t, pool, variant)); diff != "" {
					t.Errorf("refused receipt changed inventory or audit (-want +got):\n%s", diff)
				}
				form.Set("quantity", "3")
				form.Set("idempotency", retained)
				for attempt := range 2 {
					accepted := postReceipt(requestCtx, mux, form)
					if accepted.Code != http.StatusSeeOther || accepted.Header().Get("Location") != "/admin/stock/"+sku+"?received=1" {
						t.Fatalf("corrected receipt attempt %d = %d %q, want 303 to received page", attempt+1, accepted.Code, accepted.Header().Get("Location"))
					}
				}
				want := receiptSnapshot{Stock: 3, Movements: 1, Receipts: 1, Audits: 1}
				if diff := cmp.Diff(want, receiptState(t, pool, variant)); diff != "" {
					t.Errorf("corrected receipt and replay (-want +got):\n%s", diff)
				}
			})
		}
	}
}

func TestReceiptConflictStartsANewOperationAfterReviewingTheLedger(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	staff := admintest.AdminRolePool(t, pool)
	store := stock.NewStore(staff)
	mux := http.NewServeMux()
	handlerOver(store).Routes(mux, admintest.BackOffice)
	for _, tt := range []struct {
		locale i18n.Locale
		notice string
	}{
		{i18n.ZhHant, "這筆收貨與已記錄的庫存異動不符。請先確認異動紀錄，再收貨。"},
		{i18n.En, "This receipt conflicts with an already recorded stock movement. Review the ledger before receiving more stock."},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			requestCtx := i18n.WithLocale(ctx, tt.locale)
			sku, variant := receiptVariant(t, pool)
			ledger := "/admin/stock/" + sku
			get := httptest.NewRecorder()
			mux.ServeHTTP(get, httptest.NewRequestWithContext(requestCtx, http.MethodGet, ledger, nil))
			if get.Code != http.StatusOK {
				t.Fatalf("receipt page = %d, want 200", get.Code)
			}
			originalKey := receiptKey(t, get.Body.String())
			if err := store.Receive(requestCtx, sku, 3, actor.String(), originalKey); err != nil {
				t.Fatalf("record original receipt: %v", err)
			}
			original := receiptSnapshot{Stock: 3, Movements: 1, Receipts: 1, Audits: 1}
			if diff := cmp.Diff(original, receiptState(t, pool, variant)); diff != "" {
				t.Fatalf("original receipt (-want +got):\n%s", diff)
			}
			if err := store.Receive(requestCtx, sku, 4, actor.String(), originalKey); !errors.Is(err, stock.ErrMovementConflict) || !errors.Is(err, stock.ErrRefused) {
				t.Fatalf("receipt with conflicting quantity = %v, want movement conflict and refused", err)
			}
			form := url.Values{"sku": {sku}, "quantity": {"4"}, "idempotency": {originalKey}}
			conflict := postReceipt(requestCtx, mux, form)
			if conflict.Code != http.StatusSeeOther || conflict.Header().Get("Location") != ledger+"?receipt-conflict=1" {
				t.Fatalf("conflicting receipt = %d %q, want 303 to recorded-movement notice", conflict.Code, conflict.Header().Get("Location"))
			}
			if diff := cmp.Diff(original, receiptState(t, pool, variant)); diff != "" {
				t.Errorf("conflicting receipt changed inventory or audit (-want +got):\n%s", diff)
			}
			get = httptest.NewRecorder()
			mux.ServeHTTP(get, httptest.NewRequestWithContext(requestCtx, http.MethodGet, conflict.Header().Get("Location"), nil))
			if get.Code != http.StatusOK {
				t.Fatalf("recorded-movement ledger = %d, want 200", get.Code)
			}
			body := get.Body.String()
			if !strings.Contains(body, tt.notice) {
				t.Errorf("recorded-movement ledger lacks %q", tt.notice)
			}
			if strings.Contains(body, `aria-invalid="true"`) || strings.Contains(body, `id="receive-qty-error"`) {
				t.Error("new receipt form carries the earlier operation's field refusal")
			}
			freshKey := receiptKey(t, body)
			if freshKey == originalKey {
				t.Fatal("recorded-movement ledger retained the spent operation identity")
			}
			form.Set("idempotency", freshKey)
			for attempt := range 2 {
				accepted := postReceipt(requestCtx, mux, form)
				if accepted.Code != http.StatusSeeOther || accepted.Header().Get("Location") != ledger+"?received=1" {
					t.Fatalf("new receipt attempt %d = %d %q, want 303 to received page", attempt+1, accepted.Code, accepted.Header().Get("Location"))
				}
			}
			form.Set("quantity", "3")
			form.Set("idempotency", originalKey)
			replay := postReceipt(requestCtx, mux, form)
			if replay.Code != http.StatusSeeOther || replay.Header().Get("Location") != ledger+"?received=1" {
				t.Fatalf("original receipt replay = %d %q, want 303 to received page", replay.Code, replay.Header().Get("Location"))
			}
			want := receiptSnapshot{Stock: 7, Movements: 2, Receipts: 2, Audits: 2}
			if diff := cmp.Diff(want, receiptState(t, pool, variant)); diff != "" {
				t.Errorf("new receipt and both replays (-want +got):\n%s", diff)
			}
		})
	}
}

func postReceipt(ctx context.Context, mux *http.ServeMux, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/stock/receive", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func receiptKey(t *testing.T, body string) string {
	t.Helper()
	input := regexp.MustCompile(`<input[^>]*name="idempotency"[^>]*>`).FindString(body)
	key := admintest.InputAttribute(t, input, "value")
	if key == "" {
		t.Fatal("receipt form has no operation identity")
	}
	return key
}

func receiptVariant(t *testing.T, owner *pgxpool.Pool) (string, uuid.UUID) {
	t.Helper()
	var productID uuid.UUID
	if err := owner.QueryRow(t.Context(), `
    INSERT INTO products (brand_id, category_id, slug, name, status)
    SELECT b.id, c.id, $1, 'Receipt refusal test', 'draft'
    FROM brands b CROSS JOIN categories c LIMIT 1 RETURNING id`,
		"receipt-refusal-"+uuid.NewString()).Scan(&productID); err != nil {
		t.Fatalf("create receipt product: %v", err)
	}
	sku := "RECEIPT-" + strings.ToUpper(uuid.NewString())
	var variant uuid.UUID
	if err := owner.QueryRow(t.Context(), `
    INSERT INTO product_variants (product_id, sku, price_cents, safety_stock, position, is_active)
    VALUES ($1, $2, 10000, 0, 1, true) RETURNING id`, productID, sku).Scan(&variant); err != nil {
		t.Fatalf("create receipt variant: %v", err)
	}
	return sku, variant
}

type receiptSnapshot struct {
	Stock     int32
	Movements int
	Receipts  int
	Audits    int
}

func receiptState(t *testing.T, owner *pgxpool.Pool, variant uuid.UUID) receiptSnapshot {
	t.Helper()
	var state receiptSnapshot
	if err := owner.QueryRow(t.Context(), `
    SELECT v.stock_quantity,
           (SELECT count(*) FROM inventory_movements WHERE variant_id = v.id),
           (SELECT count(*) FROM inventory_movements WHERE variant_id = v.id AND reason = 'receipt'),
           (SELECT count(*) FROM audit_events WHERE entity_id = v.id AND action = 'stock.receive')
    FROM product_variants v WHERE v.id = $1`, variant).
		Scan(&state.Stock, &state.Movements, &state.Receipts, &state.Audits); err != nil {
		t.Fatalf("read receipt state: %v", err)
	}
	return state
}
