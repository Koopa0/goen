//go:build integration

package db_test

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
)

// TestOnlyTheApplicationRolesConnect logs in for real. The suites connect as
// the owner, a superuser, whom CONNECT never stops, so only a login of each
// kind shows who the database lets in: the three _svc logins and a reporting
// login through the groups they inherit from, and not a login that is only
// PUBLIC.
func TestOnlyTheApplicationRolesConnect(t *testing.T) {
	owner := dbtest.Pool(t)
	ctx := t.Context()

	var publicConnects bool
	if err := owner.QueryRow(ctx,
		`SELECT has_database_privilege('public', current_database(), 'CONNECT')`).Scan(&publicConnects); err != nil {
		t.Fatalf("read PUBLIC's CONNECT: %v", err)
	}
	if publicConnects {
		t.Error("PUBLIC holds CONNECT: every login on the cluster can read this database's catalogue")
	}

	password := strings.ToLower(rand.Text())
	for _, svc := range []string{"store_svc", "admin_svc", "maintenance_svc"} {
		// The _svc roles belong to the server and nothing else in this test
		// binary logs in as them; the password is what operations would set.
		if _, err := owner.Exec(ctx, "ALTER ROLE "+svc+" PASSWORD '"+password+"'"); err != nil {
			t.Fatalf("set %s's password: %v", svc, err)
		}
	}
	dashboard := newLogin(t, owner, "IN ROLE reporting", password)
	stranger := newLogin(t, owner, "", password)

	for _, tc := range []struct {
		login, role string // role is what the login assumes; "" refuses the login
	}{
		{"store_svc", "store"},
		{"admin_svc", "admin"},
		{"maintenance_svc", "maintenance"},
		{dashboard, "reporting"},
		{stranger, ""},
	} {
		t.Run(tc.login, func(t *testing.T) {
			cfg := owner.Config().ConnConfig.Copy()
			cfg.User, cfg.Password = tc.login, password
			conn, err := pgx.ConnectConfig(t.Context(), cfg)
			if tc.role == "" {
				if err == nil {
					_ = conn.Close(context.WithoutCancel(t.Context()))
					t.Fatalf("%s, a member of no goen role, connected to %s", tc.login, cfg.Database)
				}
				if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "42501" {
					t.Fatalf("connect as %s = %v, want insufficient_privilege (42501) for the database", tc.login, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("connect as %s: %v", tc.login, err)
			}
			defer func() { _ = conn.Close(context.WithoutCancel(t.Context())) }()
			if _, err := conn.Exec(t.Context(), "SET ROLE "+tc.role); err != nil {
				t.Fatalf("SET ROLE %s as %s: %v", tc.role, tc.login, err)
			}
			var current string
			if err := conn.QueryRow(t.Context(), "SELECT current_user::text").Scan(&current); err != nil {
				t.Fatalf("read current_user as %s: %v", tc.login, err)
			}
			if current != tc.role {
				t.Errorf("%s runs as %s after SET ROLE, want %s", tc.login, current, tc.role)
			}
		})
	}
}

// newLogin creates a login role with the given membership clause and drops it
// when the test ends. Roles belong to the server, so the name is the test's own.
func newLogin(t *testing.T, owner *pgxpool.Pool, membership, password string) string {
	t.Helper()
	name := "login_" + strings.ToLower(rand.Text()[:12])
	if _, err := owner.Exec(t.Context(),
		"CREATE ROLE "+name+" LOGIN PASSWORD '"+password+"' "+membership); err != nil {
		t.Fatalf("create login %s: %v", name, err)
	}
	t.Cleanup(func() {
		if _, err := owner.Exec(context.WithoutCancel(t.Context()), "DROP ROLE "+name); err != nil {
			t.Errorf("drop login %s: %v", name, err)
		}
	})
	return name
}

// TestHardenSchemaRefusesARoleMigrationsWouldNotCreate: 001 creates its roles
// only IF NOT EXISTS, so a cluster that already has one hands it to goen as it
// is, and the binary assumes it with SET ROLE. Each case gives one role a power
// 001 would not have given it, inside a transaction that is rolled back, since
// the roles are the whole server's.
func TestHardenSchemaRefusesARoleMigrationsWouldNotCreate(t *testing.T) {
	owner := dbtest.Pool(t)
	var schemaOwner string
	if err := owner.QueryRow(t.Context(),
		`SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'public.orders'::regclass`).Scan(&schemaOwner); err != nil {
		t.Fatalf("read the schema owner: %v", err)
	}

	harden := func(t *testing.T, change string) error {
		t.Helper()
		ctx := t.Context()
		tx, err := owner.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		if change != "" {
			if _, changeErr := tx.Exec(ctx, change); changeErr != nil {
				t.Fatalf("%s: %v", change, changeErr)
			}
		}
		_, err = tx.Exec(ctx, "CALL harden_schema()")
		return err
	}

	// As 001 left them, and again: a later migration calls it on a hardened schema.
	if err := harden(t, ""); err != nil {
		t.Fatalf("harden_schema() on the roles 001 created = %v, want it to pass", err)
	}

	for _, tc := range []struct{ role, change string }{
		{"store", "ALTER ROLE store SUPERUSER"},
		{"store", "ALTER ROLE store LOGIN"},
		{"admin", "ALTER ROLE admin CREATEROLE"},
		{"admin", "ALTER ROLE admin CREATEDB"},
		{"maintenance", "ALTER ROLE maintenance REPLICATION"},
		{"maintenance", "ALTER ROLE maintenance BYPASSRLS"},
		{"reporting", "GRANT pg_read_all_data TO reporting"},
		{"admin", "GRANT pg_write_all_data TO admin"},
		{"store", "GRANT " + pgx.Identifier{schemaOwner}.Sanitize() + " TO store"},
	} {
		t.Run(tc.change, func(t *testing.T) {
			err := harden(t, tc.change)
			pgErr, ok := errors.AsType[*pgconn.PgError](err)
			if !ok {
				t.Fatalf("harden_schema() after %s = %v, want a refusal", tc.change, err)
			}
			if pgErr.Code != "55000" || !strings.Contains(pgErr.Message, "role "+tc.role+" must be") {
				t.Errorf("harden_schema() after %s = %s %q, want 55000 naming role %s",
					tc.change, pgErr.Code, pgErr.Message, tc.role)
			}
		})
	}
}

// TestAFunctionCreatedAfterTheSweepIsClosed is what a later migration does:
// create a function the way PostgreSQL defaults it, with no grant and no
// search_path, then end on harden_schema(). The default privileges keep it from
// PUBLIC from the moment it exists; the sweep pins its search_path.
func TestAFunctionCreatedAfterTheSweepIsClosed(t *testing.T) {
	owner := dbtest.Pool(t)
	ctx := t.Context()
	if _, err := owner.Exec(ctx, `CREATE FUNCTION later_migration_probe() RETURNS int LANGUAGE sql AS 'SELECT 1'`); err != nil {
		t.Fatalf("create the function: %v", err)
	}

	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, roleErr := tx.Exec(ctx, "SET LOCAL ROLE reporting"); roleErr != nil {
		t.Fatalf("set role reporting: %v", roleErr)
	}
	_, err = tx.Exec(ctx, "SELECT later_migration_probe()")
	_ = tx.Rollback(context.WithoutCancel(ctx))
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "42501" {
		t.Errorf("reporting calls a function created after harden_schema() = %v, want insufficient_privilege (42501)", err)
	}

	if _, err := owner.Exec(ctx, "CALL harden_schema()"); err != nil {
		t.Fatalf("harden_schema(): %v", err)
	}
	var config []string
	if err := owner.QueryRow(ctx,
		`SELECT coalesce(proconfig, '{}') FROM pg_proc WHERE oid = 'later_migration_probe()'::regprocedure`).Scan(&config); err != nil {
		t.Fatalf("read the function's settings: %v", err)
	}
	if want := "search_path=pg_catalog, public, pg_temp"; len(config) != 1 || config[0] != want {
		t.Errorf("after harden_schema() the function's settings are %q, want [%q]", config, want)
	}
}
