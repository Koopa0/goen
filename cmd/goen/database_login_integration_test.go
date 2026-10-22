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

	"github.com/koopa0/goen/internal/auth"
)

// TestReachDatabaseLogin: in the secure posture each startup pool (storefront,
// back office, maintenance) refuses a login that is a superuser or a member of
// the table owner, naming the variable that configures it, and every pool that
// does start runs its session as its own role and not as a superuser.
func TestReachDatabaseLogin(t *testing.T) {
	var owner string
	if err := pool.QueryRow(t.Context(), `SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'public.orders'::regclass`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	memberships := "store, admin, maintenance"
	ownerMemberships := memberships + ", " + pgx.Identifier{owner}.Sanitize()
	plainURL := databaseLoginURL(t, memberships, "INHERIT")
	ownerURL := databaseLoginURL(t, ownerMemberships, "INHERIT")
	// A NOINHERIT member holds none of the owner's privileges until SET ROLE.
	noInheritOwnerURL := databaseLoginURL(t, ownerMemberships, "NOINHERIT")
	openers := []struct {
		role     databaseRole
		variable string
		open     func(context.Context, string, bool, *slog.Logger) (*pgxpool.Pool, error)
	}{
		{storeRole, "GOEN_DATABASE_URL", func(ctx context.Context, dsn string, secure bool, log *slog.Logger) (*pgxpool.Pool, error) {
			return servingPool(ctx, dsn, auth.DemoAccount{}, secure, log)
		}},
		{adminRole, "GOEN_ADMIN_DATABASE_URL", reachableAdminPool},
		{maintenanceRole, "GOEN_MAINTENANCE_DATABASE_URL", openMaintenancePool},
	}
	for _, opener := range openers {
		for _, login := range []struct {
			name      string
			dsn       string
			condition string
		}{
			{"plain member", plainURL, ""},
			{"owner member", ownerURL, "a member of the table owner"},
			{"no-inherit owner member", noInheritOwnerURL, "a member of the table owner"},
			{"superuser", pool.Config().ConnString(), "a superuser"},
		} {
			for _, secure := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/secure=%t", opener.role, login.name, secure), func(t *testing.T) {
					p, err := opener.open(t.Context(), login.dsn, secure, quietLog)
					if p != nil {
						defer p.Close()
					}
					if login.condition != "" && secure {
						if err == nil || p != nil {
							t.Fatalf("unsafe secure login = %v, pool present %t, want startup refusal", err, p != nil)
						}
						assertDatabaseLoginRemedy(t, err.Error(), opener.role, opener.variable, login.condition)
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

func databaseLoginURL(t *testing.T, memberships, inherit string) string {
	t.Helper()
	role := "login_posture_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := strings.ReplaceAll(uuid.NewString(), "-", "")
	create := fmt.Sprintf("CREATE ROLE %s LOGIN NOSUPERUSER %s PASSWORD '%s' IN ROLE %s", pgx.Identifier{role}.Sanitize(), inherit, password, memberships)
	if _, err := pool.Exec(t.Context(), create); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is cancelled before Cleanup runs.
		if _, err := pool.Exec(context.Background(), "DROP OWNED BY "+pgx.Identifier{role}.Sanitize()+"; DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Error(err)
		}
	})
	// The schema grants CONNECT to its roles and not to PUBLIC, and a NOINHERIT
	// login does not inherit it from them, so it is granted the way an operator
	// would; the check under test is what the login can do once connected.
	if inherit == "NOINHERIT" {
		grant := "GRANT CONNECT ON DATABASE " + pgx.Identifier{pool.Config().ConnConfig.Database}.Sanitize() + " TO " + pgx.Identifier{role}.Sanitize()
		if _, err := pool.Exec(t.Context(), grant); err != nil {
			t.Fatal(err)
		}
	}
	dsn, err := url.Parse(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	dsn.User = url.UserPassword(role, password)
	return dsn.String()
}
