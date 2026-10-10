//go:build integration

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
)

func TestEachStartupPoolChecksItsSessionLogin(t *testing.T) {
	var owner string
	if err := pool.QueryRow(t.Context(), `SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'public.orders'::regclass`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	plainURL := databaseLoginURL(t, "store, admin, maintenance")
	ownerURL := databaseLoginURL(t, "store, admin, maintenance, "+pgx.Identifier{owner}.Sanitize())
	openers := []struct {
		role databaseRole
		open func(context.Context, string, bool, *slog.Logger) (*pgxpool.Pool, error)
	}{
		{storeRole, func(ctx context.Context, dsn string, secure bool, log *slog.Logger) (*pgxpool.Pool, error) {
			return servingPool(ctx, dsn, account.DemoAccount{}, secure, log)
		}},
		{adminRole, reachableAdminPool},
		{maintenanceRole, openMaintenancePool},
	}
	for _, opener := range openers {
		for _, login := range []struct {
			name   string
			dsn    string
			unsafe bool
		}{
			{"plain member", plainURL, false},
			{"owner member", ownerURL, true},
			{"superuser", pool.Config().ConnString(), true},
		} {
			for _, secure := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/secure=%t", opener.role, login.name, secure), func(t *testing.T) {
					p, err := opener.open(t.Context(), login.dsn, secure, quietLog)
					if p != nil {
						defer p.Close()
					}
					if login.unsafe && secure {
						if err == nil || p != nil {
							t.Fatalf("unsafe secure login = %v, pool present %t, want startup refusal", err, p != nil)
						}
						assertDatabaseLoginRemedy(t, err.Error(), opener.role)
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					var role string
					var superuser bool
					if err := p.QueryRow(t.Context(), `SELECT current_user, current_setting('is_superuser')::boolean`).Scan(&role, &superuser); err != nil {
						t.Fatal(err)
					}
					if role != string(opener.role) || superuser {
						t.Errorf("application session = %s, superuser=%t, want %s, false", role, superuser, opener.role)
					}
				})
			}
		}
	}
}

func databaseLoginURL(t *testing.T, memberships string) string {
	t.Helper()
	role := "login_posture_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := strings.ReplaceAll(uuid.NewString(), "-", "")
	create := fmt.Sprintf("CREATE ROLE %s LOGIN NOSUPERUSER INHERIT PASSWORD '%s' IN ROLE %s", pgx.Identifier{role}.Sanitize(), password, memberships)
	if _, err := pool.Exec(t.Context(), create); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is cancelled before Cleanup runs.
		if _, err := pool.Exec(context.Background(), "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Error(err)
		}
	})
	dsn, err := url.Parse(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	dsn.User = url.UserPassword(role, password)
	return dsn.String()
}
