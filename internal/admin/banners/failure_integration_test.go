//go:build integration

package banners_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/banners"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
)

// TestHandler_SetBannerActiveClosed: a banner row write answers a failure as a server error. A database that cannot be
// reached decided nothing, and answering 404 or "refused" says otherwise.
func TestHandler_SetBannerActiveClosed(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	closed := admintest.NamedPool(t, pool, "banners_closed_"+uuid.NewString()[:8])
	closed.Close()
	h := banners.NewHandler(banners.NewStore(closed), slog.New(slog.DiscardHandler))
	id := uuid.NewString()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/"+id, strings.NewReader(url.Values{"active": {"1"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", id)
	res := httptest.NewRecorder()
	h.SetBannerActive(res, req)
	if res.Code != http.StatusInternalServerError {
		t.Errorf("toggle promo banner on a closed pool = %d %s, want 500", res.Code, res.Header().Get("Location"))
	}
}

// TestHandler_BannersReadFailures: banner read failures answer as server errors.
func TestHandler_BannersReadFailures(t *testing.T) {
	p := admintest.Pool(t)
	staffCtx, _ := admintest.StaffContext(t, p)
	bannerID := seedStripList(t, staffCtx, p)
	adminPool := admintest.AdminRolePool(t, p)
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(staffCtx, locale)
		for _, read := range []struct {
			name, query, lock string
		}{
			{name: "promo banners", query: "ManagedBanners", lock: "LOCK TABLE promo_banners IN ACCESS EXCLUSIVE MODE"},
		} {
			for _, page := range []struct {
				name, path string
				status     int
			}{
				{name: "GET", path: "/admin/banners", status: http.StatusOK},
				{name: "banner refusal", path: "/admin/banners", status: http.StatusUnprocessableEntity},
			} {
				t.Run(locale.Tag()+"/"+read.name+"/"+page.name, func(t *testing.T) {
					trace := &readLockTrace{query: read.query}
					cfg := adminPool.Config().Copy()
					cfg.MaxConns = 1
					cfg.ConnConfig.RuntimeParams["lock_timeout"] = "200"
					cfg.ConnConfig.Tracer = trace
					reading, err := pgxpool.NewWithConfig(t.Context(), cfg)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(reading.Close)
					var role string
					if roleErr := reading.QueryRow(ctx, `SELECT current_user`).Scan(&role); roleErr != nil || role != "admin" {
						t.Fatalf("reader role = %q, want admin: %v", role, roleErr)
					}
					var logged bytes.Buffer
					log := slog.New(slog.NewTextHandler(&logged, nil))
					mux := http.NewServeMux()
					banners.NewHandler(banners.NewStore(reading), log).Routes(mux, admintest.BackOffice)
					request := func() *http.Request {
						if page.status == http.StatusOK {
							return httptest.NewRequestWithContext(ctx, http.MethodGet, page.path, nil)
						}
						return refusedStripRequest(ctx)
					}
					control := httptest.NewRecorder()
					mux.ServeHTTP(control, request())
					if control.Code != page.status {
						t.Fatalf("unlocked editor = %d, want %d", control.Code, page.status)
					}
					assertStripList(t, ctx, control.Body.String(), bannerID)
					trace.reset()
					logged.Reset()
					holder, err := p.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Rollback(ctx, holder)
					if _, lockErr := holder.Exec(ctx, read.lock); lockErr != nil {
						t.Fatal(lockErr)
					}
					res := httptest.NewRecorder()
					mux.ServeHTTP(res, request())
					attempts := trace.snapshot()
					if len(attempts) != 1 {
						t.Fatalf("%s lock attempts = %d, want one", read.query, len(attempts))
					}
					pgErr, ok := errors.AsType[*pgconn.PgError](attempts[0])
					if !ok || pgErr.Code != "55P03" || ctx.Err() != nil {
						t.Fatalf("%s error = %v, request = %v, want SQLSTATE55P03 with a live request", read.query, attempts[0], ctx.Err())
					}
					if res.Code != http.StatusInternalServerError || res.Header().Get("Location") != "" {
						t.Errorf("locked %s editor = %d to %q, want 500 without Location", read.query, res.Code, res.Header().Get("Location"))
					}
					if !strings.Contains(logged.String(), `level=ERROR msg="read promo banners"`) {
						t.Errorf("locked editor did not log its read failure at Error: %s", logged.String())
					}
					if rollbackErr := holder.Rollback(ctx); rollbackErr != nil {
						t.Fatal(rollbackErr)
					}
					retry := httptest.NewRecorder()
					mux.ServeHTTP(retry, request())
					if retry.Code != page.status {
						t.Fatalf("released editor = %d, want %d", retry.Code, page.status)
					}
					assertStripList(t, ctx, retry.Body.String(), bannerID)
				})
			}
		}
	}
}

type readQueryKey struct{}

type readLockTrace struct {
	mu     sync.Mutex
	query  string
	errors []error
}

func (tr *readLockTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, readQueryKey{}, strings.HasPrefix(data.SQL, "-- name: "+tr.query+" :many"))
}

func (tr *readLockTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	matched, _ := ctx.Value(readQueryKey{}).(bool)
	if !matched {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.errors = append(tr.errors, data.Err)
}

func (tr *readLockTrace) reset() {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.errors = nil
}

func (tr *readLockTrace) snapshot() []error {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return slices.Clone(tr.errors)
}
