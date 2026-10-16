//go:build integration

package main

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/payment"
)

// TestStartupAndReadinessRefuseAnotherSchema starts each pool the way
// production does, through its _svc login, on a database that is or is not
// the schema this binary embeds. The binary used to start on a database built
// from an older 001 and answer 500 on the pages that read what it lacked
// (#1972); secure startup now refuses it and /readyz answers 503.
func TestStartupAndReadinessRefuseAnotherSchema(t *testing.T) {
	password := strings.ToLower(rand.Text())
	for _, svc := range []string{"store_svc", "admin_svc", "maintenance_svc"} {
		// The _svc roles belong to the server and no other test here logs in
		// as them; the password is what operations would set.
		if _, err := pool.Exec(t.Context(), "ALTER ROLE "+svc+" PASSWORD '"+password+"'"); err != nil {
			t.Fatalf("set %s's password: %v", svc, err)
		}
	}
	openers := []struct {
		login string
		open  func(context.Context, string, bool, *slog.Logger) (*pgxpool.Pool, error)
	}{
		{"store_svc", func(ctx context.Context, dsn string, secure bool, log *slog.Logger) (*pgxpool.Pool, error) {
			return servingPool(ctx, dsn, auth.DemoAccount{}, secure, log)
		}},
		{"admin_svc", reachableAdminPool},
		{"maintenance_svc", openMaintenancePool},
	}

	for _, tc := range []struct {
		name    string
		change  string // what makes the database another schema; "" leaves it as built
		refusal string
	}{
		{"as built", "", ""},
		{"one migration behind", `UPDATE schema_migrations SET version = version - 1`, "want 1"},
		{"a half-applied migration", `UPDATE schema_migrations SET dirty = true`, "did not finish"},
		{"another edition of 001", `CREATE OR REPLACE FUNCTION schema_version(OUT digest text, OUT version bigint, OUT dirty boolean)
			LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp AS
			$$ SELECT repeat('f', 64), m.version, m.dirty FROM schema_migrations m $$`, "different migrations/001"},
		{"an 001 older than schema_version()", `DROP FUNCTION schema_version()`, "no schema_version()"},
		{"not built by golang-migrate", `DROP TABLE schema_migrations`, "no golang-migrate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := dbtest.Pool(t)
			if tc.change != "" {
				if _, err := db.Exec(t.Context(), tc.change); err != nil {
					t.Fatalf("%s: %v", tc.change, err)
				}
			}
			for _, opener := range openers {
				dsn, err := url.Parse(db.Config().ConnString())
				if err != nil {
					t.Fatal(err)
				}
				dsn.User = url.UserPassword(opener.login, password)
				p, err := opener.open(t.Context(), dsn.String(), true, quietLog)
				if p != nil {
					p.Close()
				}
				switch {
				case tc.refusal == "" && err != nil:
					t.Errorf("secure start as %s = %v, want it to start", opener.login, err)
				case tc.refusal != "" && (err == nil || !strings.Contains(err.Error(), "refusing to start") || !strings.Contains(err.Error(), tc.refusal)):
					t.Errorf("secure start as %s = %v, want a refusal naming %q", opener.login, err, tc.refusal)
				}
				// Development starts and says why it should not have.
				p, err = opener.open(t.Context(), dsn.String(), false, quietLog)
				if p != nil {
					p.Close()
				}
				if err != nil {
					t.Errorf("development start as %s = %v, want a warning only", opener.login, err)
				}
			}

			wantStatus, wantBody := http.StatusOK, "ready\n"
			if tc.refusal != "" {
				wantStatus, wantBody = http.StatusServiceUnavailable, "database schema is not this binary's\n"
			}
			if status, body := readiness(t, db); status != wantStatus || body != wantBody {
				t.Errorf("GET /readyz = %d %q, want %d %q", status, body, wantStatus, wantBody)
			}
		})
	}
}

// readiness asks the router's /readyz with the store pool on db's database.
func readiness(t *testing.T, db *pgxpool.Pool) (int, string) {
	t.Helper()
	store, err := openPool(t.Context(), db.Config().ConnString(), quietLog)
	if err != nil {
		t.Fatalf("open store pool: %v", err)
	}
	defer store.Close()
	admin, err := openAdminPool(t.Context(), db.Config().ConnString(), quietLog)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer admin.Close()
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("disabled payment gateway: %v", err)
	}
	router := newRouter(&RouterConfig{
		Storefront: StorefrontConfig{StorePool: store, Payments: gateway, BaseURL: "http://127.0.0.1"},
		BackOffice: BackOfficeConfig{AdminPool: admin, Payments: gateway, Refunder: gateway},
	}, quietLog)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
	return res.Code, res.Body.String()
}
