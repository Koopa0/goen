//go:build integration

package main

import (
	"bytes"
	"compress/gzip"
	"context"
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
	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/payment"
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
	maintenancePool, err := openMaintenancePool(ctx, pool.Config().ConnString())
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
	rejected, err := openMaintenancePool(ctx, probeURL)
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

// TestEachPoolCarriesItsRoleStatementTimeout is the only bound a request has.
// http.Server's WriteTimeout does not cancel r.Context(), so nothing in Go ends
// a handler blocked in the database and enough of them starve the pool. The
// three figures are independent literals rather than the constants, because a
// timeout that reaches no session and one nobody chose look the same from here.
func TestEachPoolCarriesItsRoleStatementTimeout(t *testing.T) {
	dsn := pool.Config().ConnString()
	tests := []struct {
		role   string
		open   func(context.Context, string) (*pgxpool.Pool, error)
		wantMS int64
	}{
		{role: "store", open: openPool, wantMS: 15_000},
		{role: "admin", open: openAdminPool, wantMS: 30_000},
		// The co-purchase rebuild is measured in hundreds of milliseconds over
		// the whole order history and must outlive the storefront's bound.
		{role: "maintenance", open: openMaintenancePool, wantMS: 300_000},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			p, err := tt.open(t.Context(), dsn)
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
		Pool: counted, AdminPool: counted, Payments: gateway,
		Refunder: admin.NewRefunder(""), BaseURL: "http://127.0.0.1",
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
