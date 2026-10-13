//go:build integration

package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/twofactor"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	code := m.Run()
	stop()
	os.Exit(code)
}

// TestMaintenancePoolIsReachedAndRoleCheckedAtStartup binds both halves of the
// independent worker DSN contract. A successful return already owns a live
// connection running as maintenance; a login that cannot assume that role is
// rejected by openMaintenancePool itself, not by the first projection refresh.
func TestMaintenancePoolIsReachedAndRoleCheckedAtStartup(t *testing.T) {
	ctx := t.Context()
	maintenancePool, err := openMaintenancePool(ctx, pool.Config().ConnString(), false, quietLog)
	if err != nil {
		t.Fatalf("open reachable maintenance pool: %v", err)
	}
	defer maintenancePool.Close()
	if maintenancePool.Stat().TotalConns() == 0 {
		t.Fatal("openMaintenancePool returned without establishing a connection")
	}
	var currentRole string
	if queryErr := maintenancePool.QueryRow(ctx, `SELECT current_user`).Scan(&currentRole); queryErr != nil {
		t.Fatalf("read maintenance role: %v", queryErr)
	}
	if currentRole != "maintenance" {
		t.Fatalf("maintenance pool runs as %q, want maintenance", currentRole)
	}

	role := "maintenance_probe_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	password := "probe_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	createRole := fmt.Sprintf(
		"CREATE ROLE %s LOGIN NOSUPERUSER PASSWORD '%s'",
		pgx.Identifier{role}.Sanitize(), password,
	)
	if _, createErr := pool.Exec(ctx, createRole); createErr != nil {
		t.Fatalf("create unprivileged maintenance probe: %v", createErr)
	}
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is cancelled before Cleanup runs.
		_, _ = pool.Exec(context.Background(), "DROP ROLE "+pgx.Identifier{role}.Sanitize())
	})

	probeDSN, err := url.Parse(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	probeDSN.User = url.UserPassword(role, password)
	probeURL := probeDSN.String()
	rejected, err := openMaintenancePool(ctx, probeURL, false, quietLog)
	if rejected != nil {
		rejected.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "maintenance") {
		t.Fatalf("openMaintenancePool with a login outside maintenance = %v, want startup refusal", err)
	}
	if strings.Contains(err.Error(), password) || strings.Contains(err.Error(), probeURL) {
		t.Errorf("maintenance startup error leaked its DSN credential: %v", err)
	}
}

// TestEachPoolCarriesItsRoleStatementTimeout is the PostgreSQL-side bound on an
// acquired connection. storeRequestBudget ends pool waits before WriteTimeout;
// these figures are independent literals rather than the constants, because a
// timeout that reaches no session and one nobody chose look the same from here.
func TestEachPoolCarriesItsRoleStatementTimeout(t *testing.T) {
	dsn := pool.Config().ConnString()
	tests := []struct {
		role   string
		open   func(context.Context, string, *slog.Logger) (*pgxpool.Pool, error)
		wantMS int64
	}{
		{role: "store", open: openPool, wantMS: 15_000},
		{role: "admin", open: openAdminPool, wantMS: 30_000},
		// The co-purchase rebuild is measured in hundreds of milliseconds over
		// the whole order history and must outlive the storefront's bound.
		{role: "maintenance", open: func(ctx context.Context, url string, log *slog.Logger) (*pgxpool.Pool, error) {
			return openMaintenancePool(ctx, url, false, log)
		}, wantMS: 300_000},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			p, err := tt.open(t.Context(), dsn, quietLog)
			if err != nil {
				t.Fatalf("open %s pool: %v", tt.role, err)
			}
			defer p.Close()

			var timeoutMS int64
			if scanErr := p.QueryRow(t.Context(),
				`SELECT setting::bigint FROM pg_settings WHERE name = 'statement_timeout'`,
			).Scan(&timeoutMS); scanErr != nil {
				t.Fatalf("read %s statement_timeout: %v", tt.role, scanErr)
			}
			if timeoutMS != tt.wantMS {
				t.Errorf("%s session statement_timeout = %d ms, want %d", tt.role, timeoutMS, tt.wantMS)
			}
		})
	}
}

type countingTracer struct{ queries *atomic.Int64 }

func (t countingTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	t.queries.Add(1)
	return ctx
}

func (countingTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestTheRouterKeepsAssetsStatelessAndCompressesPages(t *testing.T) {
	var queries atomic.Int64
	poolConfig := pool.Config()
	poolConfig.ConnConfig.Tracer = countingTracer{queries: &queries}
	counted, err := pgxpool.NewWithConfig(t.Context(), poolConfig)
	if err != nil {
		t.Fatalf("open traced pool: %v", err)
	}
	t.Cleanup(counted.Close)

	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("build disabled payment gateway: %v", err)
	}
	router := newRouter(&RouterConfig{
		Storefront: StorefrontConfig{
			StorePool: counted, Payments: gateway, BaseURL: "http://127.0.0.1",
		},
		BackOffice: BackOfficeConfig{
			AdminPool: counted, Payments: gateway, Refunder: refunds.NewRefunder(""),
		},
	}, slog.New(slog.DiscardHandler))

	serve := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		queries.Store(0)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
		req.Header.Set("Accept-Encoding", "gzip")
		req.Header.Set("Cookie", "goen_cart=notarealtoken; goen_session=notarealtoken")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}

	asset := serve(assets.URL(assets.AppCSS))
	if asset.Code != http.StatusOK {
		t.Fatalf("asset status = %d, want 200", asset.Code)
	}
	if got := queries.Load(); got != 0 {
		t.Errorf("asset request made %d database queries, want 0", got)
	}
	if got := asset.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("asset Content-Encoding = %q, want gzip", got)
	}
	if got := integrationGunzip(t, asset.Body.Bytes()); !bytes.Contains(got, []byte(".goen-header__bar")) {
		t.Error("gunzipped asset is not the application stylesheet")
	}
	if got := asset.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("asset X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := asset.Header().Get("Content-Security-Policy"); got == "" {
		t.Error("asset lost Content-Security-Policy")
	}
	if !integrationHeaderToken(asset.Header(), "Vary", "Accept-Encoding") {
		t.Errorf("asset Vary = %q, want Accept-Encoding", asset.Header().Values("Vary"))
	}
	for _, forbidden := range []string{"Cookie", "Accept-Language"} {
		if integrationHeaderToken(asset.Header(), "Vary", forbidden) {
			t.Errorf("asset Vary = %q, must not contain %s", asset.Header().Values("Vary"), forbidden)
		}
	}

	media := serve("/media/00112233445566778899aabbccddeeff")
	if media.Code != http.StatusNotFound {
		t.Errorf("unknown media status = %d, want 404", media.Code)
	}
	if got := queries.Load(); got != 0 {
		t.Errorf("media request made %d database queries, want 0", got)
	}

	page := serve("/")
	if page.Code != http.StatusOK {
		t.Fatalf("home status = %d, want 200", page.Code)
	}
	if got := queries.Load(); got == 0 {
		t.Error("home request made no database queries; the tracer cannot prove the asset zero")
	}
	if got := page.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("home Content-Encoding = %q, want gzip from the production chain", got)
	}
	if got := integrationGunzip(t, page.Body.Bytes()); !bytes.Contains(got, []byte("<!doctype html>")) {
		t.Error("gunzipped home response is not HTML")
	}
}

// TestTheRouterKeepsNoSignedInPageInAnyCache is withNoStore through the real
// chain and a real session. The signed-in home page is the case only this test
// can see: "/" is on no private prefix, so its no-store proves the user
// Authenticate resolved reaches the middleware. The seed page is the one the
// Back button must never bring back.
func TestTheRouterKeepsNoSignedInPageInAnyCache(t *testing.T) {
	ctx := t.Context()
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("build disabled payment gateway: %v", err)
	}
	router := newRouter(&RouterConfig{
		Storefront: StorefrontConfig{
			StorePool: pool, Payments: gateway, BaseURL: "http://127.0.0.1",
		},
		BackOffice: BackOfficeConfig{
			AdminPool: pool, Payments: gateway, Refunder: refunds.NewRefunder(""), TOTPKey: bytes.Repeat([]byte{7}, 32),
		},
	}, slog.New(slog.DiscardHandler))

	var staffID string
	if insertErr := pool.QueryRow(ctx, `INSERT INTO users (email, role) VALUES ($1, 'staff') RETURNING id`,
		"nostore-"+uuid.NewString()+"@example.com").Scan(&staffID); insertErr != nil {
		t.Fatalf("create staff: %v", insertErr)
	}
	token, err := account.NewStore(pool).StartSession(ctx, staffID, "test", "127.0.0.1")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	serve := func(method, path string, signedIn bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, method, path, http.NoBody)
		if signedIn {
			req.Header.Set("Cookie", "goen_session="+token)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}

	for _, tt := range []struct {
		method, path string
		signedIn     bool
		wantStatus   int
		want         string
	}{
		{http.MethodGet, "/", true, http.StatusOK, "no-store"},
		{http.MethodGet, "/account", true, http.StatusOK, "no-store"},
		{http.MethodPost, "/admin/verify/enrol", true, http.StatusOK, "no-store"},
		{http.MethodGet, "/admin/orders", true, http.StatusSeeOther, "no-store"},
		{http.MethodGet, "/checkout", false, http.StatusSeeOther, "no-store"},
		{http.MethodGet, "/", false, http.StatusOK, ""},
	} {
		res := serve(tt.method, tt.path, tt.signedIn)
		if res.Code != tt.wantStatus {
			t.Errorf("%s %s signedIn=%v answered %d, want %d; the header below would "+
				"describe a different page", tt.method, tt.path, tt.signedIn, res.Code, tt.wantStatus)
		}
		if got := res.Header().Get("Cache-Control"); got != tt.want {
			t.Errorf("%s %s signedIn=%v: Cache-Control = %q, want %q",
				tt.method, tt.path, tt.signedIn, got, tt.want)
		}
		if tt.path == "/admin/verify/enrol" && !strings.Contains(res.Body.String(), "otpauth://") {
			t.Error("the enrolment answer carries no seed; its no-store was measured on another page")
		}
	}
}

func integrationGunzip(t *testing.T, body []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("open gzip response: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read gzip response: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close gzip response: %v", err)
	}
	return got
}

func integrationHeaderToken(h http.Header, name, want string) bool {
	for _, line := range h.Values(name) {
		for token := range strings.SplitSeq(line, ",") {
			if strings.EqualFold(strings.TrimSpace(token), want) {
				return true
			}
		}
	}
	return false
}

// countingSender records what would have gone out.
type countingSender struct{ sent int }

func (s *countingSender) Send(context.Context, *email.Message) error {
	s.sent++
	return nil
}

// TestAnUnsubscribeDuringTheDrainStopsTheCopy is the lock on main's own consent
// gate. The send freezes one outbox row per subscriber and the queue drains at
// bulk priority behind every transactional message, so an unsubscribe committing
// anywhere in that window has to be read at DELIVERY.
func TestAnUnsubscribeDuringTheDrainStopsTheCopy(t *testing.T) {
	ctx := t.Context()
	subscribers := newsletter.NewStore(pool)
	sender := &countingSender{}
	deliver := newsletterIssueHandler(subscribers,
		email.New(sender, "https://goen.test", "", ""))

	address := "drain-" + uuid.NewString()[:12] + "@goen.invalid"
	token := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO newsletter_subscribers (email, unsubscribe_token)
		VALUES ($1, $2)`, address, token); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	payload := &email.NewsletterIssue{
		Email: address, UnsubscribeToken: token,
		Subject: "本週選品", Body: "內容", Locale: "zh-Hant",
	}

	if deliverErr := deliver(ctx, payload); deliverErr != nil {
		t.Fatalf("deliver to somebody who wants it: %v", deliverErr)
	}
	if sender.sent != 1 {
		t.Fatalf("a subscriber on the list got %d copies, want 1 — this proves "+
			"nothing about the refusal below", sender.sent)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE newsletter_subscribers SET unsubscribed_at = now() WHERE email = $1`,
		address); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}

	if deliverErr := deliver(ctx, payload); deliverErr != nil {
		t.Fatalf("deliver to somebody who left: %v — leaving is not a failure, and "+
			"rescheduling would retry the one thing that must not happen", deliverErr)
	}
	if sender.sent != 1 {
		t.Errorf("%d copies sent; the second went to an address that had "+
			"unsubscribed while the queue was draining", sender.sent)
	}
}

// TestTheStoreMapReturnCostsNoDatabaseRoundTrip measures what the routing
// decision beside it claims. The return route is the one door open to an
// unauthenticated cross-site POST from anybody, so a chrome query on it is a
// database round trip an attacker can ask for at no cost to themselves.
//
// A predicate test can pin that the route is not a nav path; only a traced pool
// can show that the middleware still running on it — the session reader and the
// cart badge — asks the database nothing on a request carrying no cookies.
func TestTheStoreMapReturnCostsNoDatabaseRoundTrip(t *testing.T) {
	var queries atomic.Int64
	poolConfig := pool.Config()
	poolConfig.ConnConfig.Tracer = countingTracer{queries: &queries}
	counted, err := pgxpool.NewWithConfig(t.Context(), poolConfig)
	if err != nil {
		t.Fatalf("open traced pool: %v", err)
	}
	t.Cleanup(counted.Close)

	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("build disabled payment gateway: %v", err)
	}
	// Any id: goen compares it against what a callback repeats and nothing else.
	storeMap, err := cart.NewStoreMap("1000001", string(cart.ModeB2C), "", "https://goen.test")
	if err != nil {
		t.Fatalf("build the store map: %v", err)
	}
	router := newRouter(&RouterConfig{
		Storefront: StorefrontConfig{
			StorePool: counted, Payments: gateway, BaseURL: "https://goen.test", StoreMap: storeMap,
		},
		BackOffice: BackOfficeConfig{
			AdminPool: counted, Payments: gateway, Refunder: refunds.NewRefunder(""), StoreMap: storeMap,
		},
	}, slog.New(slog.DiscardHandler))

	form := url.Values{
		"MerchantID": {"1000001"}, "MerchantTradeNo": {"ABCDEFGHIJ1234567890"},
		"LogisticsSubType": {"UNIMART"}, "CVSStoreID": {"131386"},
		"CVSStoreName": {"南港園區"}, "CVSAddress": {"台北市南港區三重路19-2號"},
		"CVSOutSide": {"0"}, "ExtraData": {"0123456789abcdef0123"},
	}
	queries.Store(0)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		cart.PickupReturnPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("the return route answered %d, want 200; the count below would "+
			"then be measuring a refusal", res.Code)
	}
	if got := queries.Load(); got != 0 {
		t.Errorf("an anonymous cross-site post to the return route made %d database "+
			"queries, want 0", got)
	}

	// The control: a storefront page on the same router does query, so the zero
	// above is a fact about this route rather than about the tracer.
	queries.Store(0)
	home := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	router.ServeHTTP(httptest.NewRecorder(), home)
	if queries.Load() == 0 {
		t.Error("the home page made no database queries either; the tracer proves nothing")
	}
}

type healthCountTracer struct {
	reads             atomic.Int64
	workerHealthReads atomic.Int64
	cancelQuery       string
	triggered         atomic.Bool
	cancelled         atomic.Bool
}

func (f *healthCountTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "-- name: WorkerHealth :one") {
		f.workerHealthReads.Add(1)
	}
	countQuery := strings.HasPrefix(data.SQL, "-- name: UnreconciledPaymentCount :one") ||
		strings.HasPrefix(data.SQL, "-- name: StrandedInvoiceClaims :many") ||
		strings.HasPrefix(data.SQL, "-- name: UninvoicedOrders :many")
	if !countQuery {
		return ctx
	}
	f.reads.Add(1)
	if f.cancelQuery != "" && strings.HasPrefix(data.SQL, "-- name: "+f.cancelQuery+" ") {
		f.triggered.Store(true)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return cancelled
	}
	return ctx
}

func (f *healthCountTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if errors.Is(ctx.Err(), context.Canceled) && errors.Is(data.Err, context.Canceled) {
		f.cancelled.Store(true)
	}
}

func TestTheRouterReadsHealthCountOnlyForVerifiedStaffAndSurvivesItsFailure(t *testing.T) {
	ctx := t.Context()
	trace := &healthCountTracer{}
	adminPool := admintest.AdminRolePool(t, pool)
	config := adminPool.Config().Copy()
	config.ConnConfig.Tracer = trace
	traced, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open traced pool: %v", err)
	}
	t.Cleanup(traced.Close)
	var role string
	var superuser bool
	if queryErr := traced.QueryRow(ctx, `SELECT current_user, current_setting('is_superuser')::boolean`).Scan(&role, &superuser); queryErr != nil {
		t.Fatalf("read traced admin role: %v", queryErr)
	}
	if role != "admin" || superuser {
		t.Fatalf("traced pool role=%q superuser=%v, want admin/false", role, superuser)
	}
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("disabled payment gateway: %v", err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	router := newRouter(&RouterConfig{
		Storefront: StorefrontConfig{StorePool: pool, Payments: gateway, BaseURL: "http://127.0.0.1"},
		BackOffice: BackOfficeConfig{AdminPool: traced, Payments: gateway, Refunder: refunds.NewRefunder(""), TOTPKey: key},
	}, slog.New(slog.DiscardHandler))
	startSession := func(role string, verified bool) string {
		t.Helper()
		var id string
		if createErr := pool.QueryRow(ctx, `INSERT INTO users (email, role) VALUES ($1, $2) RETURNING id`,
			"health-count-"+uuid.NewString()+"@example.com", role).Scan(&id); createErr != nil {
			t.Fatalf("create %s: %v", role, createErr)
		}
		token, sessionErr := account.NewStore(pool).StartSession(ctx, id, "test", "127.0.0.1")
		if sessionErr != nil {
			t.Fatalf("start %s session: %v", role, sessionErr)
		}
		if verified {
			if verifyErr := twofactor.NewStore(pool, key).MarkVerified(ctx, token); verifyErr != nil {
				t.Fatalf("verify session: %v", verifyErr)
			}
		}
		return token
	}
	serve := func(token string) *httptest.ResponseRecorder {
		t.Helper()
		trace.reads.Store(0)
		trace.workerHealthReads.Store(0)
		trace.triggered.Store(false)
		trace.cancelled.Store(false)
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/products", http.NoBody)
		req.Header.Set("Accept-Language", "en")
		if token != "" {
			req.Header.Set("Cookie", "goen_session="+token)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	for _, tt := range []struct {
		name, token string
		status      int
	}{
		{"anonymous", "", http.StatusNotFound},
		{"customer", startSession("customer", false), http.StatusNotFound},
		{"unverified staff", startSession("staff", false), http.StatusSeeOther},
	} {
		res := serve(tt.token)
		if res.Code != tt.status || trace.reads.Load() != 0 {
			t.Errorf("%s status=%d count reads=%d, want %d/0", tt.name, res.Code, trace.reads.Load(), tt.status)
		}
		if tt.status == http.StatusSeeOther && res.Header().Get("Location") != "/admin/verify" {
			t.Errorf("unverified staff Location=%q", res.Header().Get("Location"))
		}
	}
	token := startSession("staff", true)
	count, err := health.NewStore(adminPool).WithInvoicing(false).StaffTaskCount(ctx)
	if err != nil {
		t.Fatalf("read expected count: %v", err)
	}
	want := i18n.Count(i18n.WithLocale(ctx, i18n.En), i18n.KeyAdminHPPendingTasks, count, count)
	res := serve(token)
	if res.Code != http.StatusOK || trace.reads.Load() != 3 || !strings.Contains(res.Body.String(), want) {
		t.Fatalf("verified staff status=%d count reads=%d known count %q present=%v", res.Code, trace.reads.Load(), want, strings.Contains(res.Body.String(), want))
	}
	if got := trace.workerHealthReads.Load(); got != 0 {
		t.Errorf("verified staff navigation executed %d engineering WorkerHealth queries, want 0", got)
	}
	for index, query := range []string{"UnreconciledPaymentCount", "StrandedInvoiceClaims", "UninvoicedOrders"} {
		trace.cancelQuery = query
		res = serve(token)
		if !trace.triggered.Load() || !trace.cancelled.Load() || trace.reads.Load() != int64(index+1) {
			t.Fatalf("%s cancellation not observed: triggered=%v cancelled=%v reads=%d", query, trace.triggered.Load(), trace.cancelled.Load(), trace.reads.Load())
		}
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Task count unavailable") {
			t.Errorf("%s count failure: products status=%d, unknown count present=%v", query, res.Code, strings.Contains(res.Body.String(), "Task count unavailable"))
		}
		if strings.Contains(res.Body.String(), "0 tasks need attention") {
			t.Errorf("%s count failure was shown as zero", query)
		}
		if ctx.Err() != nil {
			t.Fatalf("count fault cancelled the whole request: %v", ctx.Err())
		}
	}
	trace.cancelQuery = ""
	res = serve(token)
	if res.Code != http.StatusOK || trace.reads.Load() != 3 || !strings.Contains(res.Body.String(), want) || strings.Contains(res.Body.String(), "Task count unavailable") {
		t.Errorf("navigation did not recover: status=%d reads=%d", res.Code, trace.reads.Load())
	}
}
