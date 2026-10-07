//go:build integration

package cart_test

import (
	"bytes"
	"context"
	"errors"
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

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

func TestCartLookupFailuresDoNotReplaceOrHideTheBasket(t *testing.T) {
	owner := dbtest.Pool(t)
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, seedErr := owner.Exec(t.Context(), string(seed)); seedErr != nil {
		t.Fatal(seedErr)
	}
	password := "a sufficiently long password"
	hash, err := account.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	var variant uuid.UUID
	if variantErr := owner.QueryRow(t.Context(), `SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE p.status = 'active' AND pv.is_active AND pv.stock_quantity > pv.safety_stock ORDER BY pv.id LIMIT 1`).Scan(&variant); variantErr != nil {
		t.Fatal(variantErr)
	}
	trace := &failCartLookup{}
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
	if roleErr := app.QueryRow(t.Context(), "SELECT current_user").Scan(&role); roleErr != nil || role != "store" {
		t.Fatalf("cart role = %q, error %v, want store", role, roleErr)
	}
	s := cart.NewStore(app)
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, scenario := range []string{"page", "add", "update", "checkout", "place", "pickup-start", "pickup-map", "badge", "account-fallback", "signin", "retry-adoption"} {
			t.Run(locale.Tag()+"/"+scenario, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				customerEmail := "cart-failure-" + uuid.NewString() + "@example.com"
				var customer uuid.UUID
				if customerErr := owner.QueryRow(ctx, `INSERT INTO users (email, password_hash, email_verified_at) VALUES ($1, $2, now()) RETURNING id`, customerEmail, hash).Scan(&customer); customerErr != nil {
					t.Fatal(customerErr)
				}
				id, token := newCartSession(t, s)
				if addErr := s.Add(ctx, id, variant, 1); addErr != nil {
					t.Fatal(addErr)
				}
				before := cartLookupRows(t, owner, id, variant)
				var diagnostics bytes.Buffer
				logger := slog.New(slog.NewJSONHandler(&diagnostics, nil))
				h := cart.NewHandler(s, orderaccess.NewStore(app, false), logger, false, testLimiter(), nil, nil)
				accounts := account.NewHandler(account.NewStore(app), h, logger, false, nil)
				method, path, handler := http.MethodGet, "/cart", http.HandlerFunc(h.Page)
				form := url.Values{"variant": {variant.String()}, "quantity": {"1"}, "remove": {"1"}}
				query := "CartByToken"
				wantStatus, wantLocation := http.StatusInternalServerError, ""
				switch scenario {
				case "add":
					method, path, handler = http.MethodPost, "/cart/items", h.AddItem
				case "update":
					method, path, handler = http.MethodPost, "/cart/items/update", h.UpdateItem
				case "checkout":
					path, handler = "/checkout", h.Checkout
				case "place":
					method, path, handler = http.MethodPost, "/checkout", h.PlaceOrder
				case "pickup-start":
					method, path, handler = http.MethodPost, "/checkout/pickup/start", h.PickupStart
				case "pickup-map":
					path, handler = "/checkout/pickup/map", h.PickupMap
				case "badge":
					wantStatus = http.StatusNoContent
					handler = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
				case "account-fallback":
					method, path, handler = http.MethodPost, "/cart/items", h.AddItem
					query = "CartForUser"
					ctx = user.NewContext(ctx, user.User{ID: customer.String(), Email: customerEmail, Role: user.RoleCustomer})
					token = "missing-" + token
				case "signin", "retry-adoption":
					method, path, handler = http.MethodPost, "/signin", accounts.SignIn
					form = url.Values{"email": {customerEmail}, "password": {password}, "next": {"/checkout"}}
					wantStatus, wantLocation = http.StatusSeeOther, "/account/cart-recovery?next=%2Fcheckout"
					if scenario == "retry-adoption" {
						path, handler = "/account/cart-recovery", accounts.RetryCartAdoption
						ctx = user.NewContext(ctx, user.User{ID: customer.String(), Email: customerEmail, Role: user.RoleCustomer})
					}
				}
				trace.set(query)
				req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cookie under test
				res := httptest.NewRecorder()
				var route http.Handler = handler
				if scenario == "badge" {
					route = h.WithCount(route)
				}
				route.ServeHTTP(res, req)
				trace.set("")
				if res.Code != wantStatus || res.Header().Get("Location") != wantLocation {
					t.Errorf("lookup failure = %d Location %q, want %d %q", res.Code, res.Header().Get("Location"), wantStatus, wantLocation)
				}
				for _, cookie := range res.Result().Cookies() {
					if cookie.Name == "goen_cart" {
						t.Errorf("storage failure replaced the cart cookie: %s", cookie)
					}
				}
				if wantStatus == http.StatusInternalServerError && !strings.Contains(res.Body.String(), i18n.T(ctx, i18n.KeyCartUnavailable)) {
					t.Error("storage failure did not render the generic cart recovery message")
				}
				if diagnostics.Len() == 0 || strings.Contains(res.Body.String(), "context canceled") {
					t.Errorf("lookup failure diagnostics = %q; response must keep the cause private", diagnostics.String())
				}
				if diff := cmp.Diff(before, cartLookupRows(t, owner, id, variant)); diff != "" {
					t.Errorf("cart rows after lookup failure (-want +got):\n%s", diff)
				}
				if got, lookupErr := s.ByToken(ctx, token, uuid.NullUUID{}); scenario != "account-fallback" && (lookupErr != nil || got != id) {
					t.Errorf("preserved token = %s, error %v, want %s", got, lookupErr, id)
				}
				if scenario == "signin" {
					follow := httptest.NewRequestWithContext(ctx, http.MethodGet, "/account", nil)
					for _, cookie := range res.Result().Cookies() {
						follow.AddCookie(cookie)
					}
					u, sessionErr := account.NewStore(app).SessionUser(ctx, account.ReadSessionCookie(follow, false))
					if sessionErr != nil || u.ID != customer.String() {
						t.Errorf("sign-in session = %q, error %v, want authenticated customer %s", u.ID, sessionErr, customer)
					}
				}
				// The query-local fault never cancelled the request or made insertion unavailable.
				if ctx.Err() != nil {
					t.Fatalf("lookup injection cancelled the original request: %v", ctx.Err())
				}
				if _, createErr := s.Create(ctx, "available-create-"+uuid.NewString(), uuid.NullUUID{}); createErr != nil {
					t.Fatalf("create path was unavailable after the lookup failure: %v", createErr)
				}
			})
		}
	}
	for _, tt := range []struct {
		name, method, path string
		handler            func(*cart.Handler) http.HandlerFunc
	}{
		{name: "page", method: http.MethodGet, path: "/cart", handler: func(h *cart.Handler) http.HandlerFunc { return h.Page }},
		{name: "add", method: http.MethodPost, path: "/cart/items", handler: func(h *cart.Handler) http.HandlerFunc { return h.AddItem }},
		{name: "update", method: http.MethodPost, path: "/cart/items/update", handler: func(h *cart.Handler) http.HandlerFunc { return h.UpdateItem }},
		{name: "checkout", method: http.MethodGet, path: "/checkout", handler: func(h *cart.Handler) http.HandlerFunc { return h.Checkout }},
		{name: "place", method: http.MethodPost, path: "/checkout", handler: func(h *cart.Handler) http.HandlerFunc { return h.PlaceOrder }},
		{name: "pickup-start", method: http.MethodPost, path: "/checkout/pickup/start", handler: func(h *cart.Handler) http.HandlerFunc { return h.PickupStart }},
		{name: "pickup-map", method: http.MethodGet, path: "/checkout/pickup/map", handler: func(h *cart.Handler) http.HandlerFunc { return h.PickupMap }},
		{name: "badge", method: http.MethodGet, path: "/cart"},
	} {
		t.Run("disconnect/"+tt.name, func(t *testing.T) {
			id, token := newCartSession(t, s)
			if addErr := s.Add(t.Context(), id, variant, 1); addErr != nil {
				t.Fatal(addErr)
			}
			before := cartLookupRows(t, owner, id, variant)
			var diagnostics bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&diagnostics, nil))
			h := cart.NewHandler(s, orderaccess.NewStore(app, false), logger, false, testLimiter(), nil, nil)
			var continued bool
			route := h.WithCount(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { continued = true }))
			if tt.handler != nil {
				route = tt.handler(h)
			}
			requestCtx, cancel := context.WithCancel(t.Context())
			defer cancel()
			trace.disconnect("CartByToken", cancel)
			form := url.Values{"variant": {variant.String()}, "quantity": {"1"}}
			req := httptest.NewRequestWithContext(requestCtx, tt.method, tt.path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cookie under test
			res := httptest.NewRecorder()
			route.ServeHTTP(res, req)
			trace.set("")
			if requestCtx.Err() != context.Canceled {
				t.Fatalf("disconnect context = %v, want context.Canceled", requestCtx.Err())
			}
			if diagnostics.Len() != 0 || res.Body.Len() != 0 || len(res.Header()) != 0 || continued != (tt.name == "badge") {
				t.Errorf("disconnected lookup: diagnostics=%q body=%q headers=%v continued=%v, want quiet diagnostics and badge continuation only", diagnostics.String(), res.Body.String(), res.Header(), continued)
			}
			if diff := cmp.Diff(before, cartLookupRows(t, owner, id, variant)); diff != "" {
				t.Errorf("cart rows after disconnect (-want +got):\n%s", diff)
			}
		})
	}
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, tt := range []struct {
			name          string
			failCount     bool
			cancelRequest bool
			wantCount     int
		}{
			{name: "disconnect", failCount: true, cancelRequest: true},
			{name: "query failure", failCount: true},
			{name: "success", wantCount: 1},
		} {
			t.Run("count/"+locale.Tag()+"/"+tt.name, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				id, token := newCartSession(t, s)
				if addErr := s.Add(ctx, id, variant, 1); addErr != nil {
					t.Fatal(addErr)
				}
				before := cartLookupRows(t, owner, id, variant)
				var diagnostics bytes.Buffer
				logger := slog.New(slog.NewJSONHandler(&diagnostics, nil))
				h := cart.NewHandler(s, orderaccess.NewStore(app, false), logger, false, testLimiter(), nil, nil)
				requestCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				trace.set("")
				if tt.cancelRequest {
					trace.disconnect("CartItemCount", cancel)
				} else if tt.failCount {
					trace.set("CartItemCount")
				}
				continued, count := 0, -1
				var nextErr error
				route := h.WithCount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					continued++
					count, nextErr = web.CartCount(r.Context()), r.Context().Err()
					w.WriteHeader(http.StatusNoContent)
				}))
				req := httptest.NewRequestWithContext(requestCtx, http.MethodGet, "/cart", http.NoBody)
				req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cookie under test
				res := httptest.NewRecorder()
				route.ServeHTTP(res, req)
				witness := trace.countWitness()
				trace.set("")
				if !witness.lookupSucceeded || !witness.countStarted || !witness.countAfterLookup || !witness.countFinished {
					t.Fatalf("item-count query did not follow a successful cart lookup: %+v", witness)
				}
				if tt.failCount && !errors.Is(witness.countErr, context.Canceled) {
					t.Fatalf("item-count injection did not fail the actual query: %v", witness.countErr)
				}
				if !tt.failCount && witness.countErr != nil {
					t.Fatalf("successful item-count query failed: %v", witness.countErr)
				}
				if tt.cancelRequest && !errors.Is(requestCtx.Err(), context.Canceled) {
					t.Fatalf("item-count disconnect did not cancel the parent request: %v", requestCtx.Err())
				}
				if !tt.cancelRequest && requestCtx.Err() != nil {
					t.Fatalf("query-local failure cancelled the parent request: %v", requestCtx.Err())
				}
				switch {
				case tt.cancelRequest:
					if diagnostics.Len() != 0 {
						t.Errorf("cancelled item-count request logged an error: %s", diagnostics.String())
					}
				case tt.failCount:
					if !strings.Contains(diagnostics.String(), `"level":"ERROR"`) || !strings.Contains(diagnostics.String(), `"msg":"count cart items"`) {
						t.Errorf("live request lost the item-count error diagnostic: %s", diagnostics.String())
					}
				case diagnostics.Len() != 0:
					t.Errorf("successful item count logged a diagnostic: %s", diagnostics.String())
				}
				if continued != 1 || count != tt.wantCount || res.Code != http.StatusNoContent || errors.Is(nextErr, context.Canceled) != tt.cancelRequest {
					t.Errorf("item-count next: calls=%d count=%d status=%d context=%v, want 1, %d, 204, cancelled=%v", continued, count, res.Code, nextErr, tt.wantCount, tt.cancelRequest)
				}
				if res.Body.Len() != 0 || len(res.Header()) != 0 {
					t.Errorf("item-count middleware wrote a response or cookie: body=%q headers=%v", res.Body.String(), res.Header())
				}
				if diff := cmp.Diff(before, cartLookupRows(t, owner, id, variant)); diff != "" {
					t.Errorf("cart rows after item count (-want +got):\n%s", diff)
				}
				if got, lookupErr := s.ByToken(ctx, token, uuid.NullUUID{}); lookupErr != nil || got != id {
					t.Errorf("preserved count token = %s, error %v, want %s", got, lookupErr, id)
				}
			})
		}
	}
}

type failCartLookup struct {
	mu     sync.Mutex
	query  string
	cancel context.CancelFunc
	count  cartCountWitness
}

func (tr *failCartLookup) set(query string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.query = query
	tr.cancel = nil
	tr.count = cartCountWitness{}
}

func (tr *failCartLookup) disconnect(query string, cancel context.CancelFunc) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.query, tr.cancel = query, cancel
	tr.count = cartCountWitness{}
}

func (tr *failCartLookup) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	name := ""
	for _, candidate := range []string{"CartByToken", "CartItemCount"} {
		if strings.HasPrefix(data.SQL, "-- name: "+candidate+" :one") {
			name = candidate
			break
		}
	}
	if name != "" {
		ctx = context.WithValue(ctx, cartCountQueryKey{}, name)
	}
	tr.mu.Lock()
	query, cancelRequest := tr.query, tr.cancel
	if name == "CartItemCount" {
		tr.count.countStarted = true
		tr.count.countAfterLookup = tr.count.lookupSucceeded
	}
	tr.mu.Unlock()
	if query != "" && strings.HasPrefix(data.SQL, "-- name: "+query+" :one") {
		if cancelRequest != nil {
			cancelRequest()
			return ctx
		}
		refused, cancel := context.WithCancel(ctx)
		cancel()
		return refused
	}
	return ctx
}

type cartCountQueryKey struct{}

type cartCountWitness struct {
	lookupSucceeded  bool
	countStarted     bool
	countAfterLookup bool
	countFinished    bool
	countErr         error
}

func (tr *failCartLookup) countWitness() cartCountWitness {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.count
}

func (tr *failCartLookup) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	name, _ := ctx.Value(cartCountQueryKey{}).(string)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	switch name {
	case "CartByToken":
		tr.count.lookupSucceeded = data.Err == nil
	case "CartItemCount":
		tr.count.countFinished, tr.count.countErr = true, data.Err
	}
}

func cartLookupRows(t *testing.T, p *pgxpool.Pool, id, variant uuid.UUID) []string {
	t.Helper()
	got := make([]string, 8)
	err := p.QueryRow(t.Context(), `SELECT (SELECT count(*)::text FROM carts), (SELECT coalesce(sum(quantity),0)::text FROM cart_items), (SELECT coalesce(user_id::text,'guest') FROM carts WHERE id=$1), (SELECT quantity::text FROM cart_items WHERE cart_id=$1 AND variant_id=$2), (SELECT stock_quantity::text FROM product_variants WHERE id=$2), (SELECT (checkout_draft IS NULL)::text FROM carts WHERE id=$1), (SELECT count(*)::text FROM orders), (SELECT count(*)::text FROM checkout_attempts)`, id, variant).Scan(&got[0], &got[1], &got[2], &got[3], &got[4], &got[5], &got[6], &got[7])
	if err != nil {
		t.Fatal(err)
	}
	return got
}
