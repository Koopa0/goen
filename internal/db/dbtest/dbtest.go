//go:build integration

// Package dbtest runs integration tests against a real PostgreSQL.
//
// A test binary starts one server, from its TestMain, with Start. The server
// holds a template database with every migration applied, and each database a
// test gets, the one Start returns included, is a copy of that template made by
// CREATE DATABASE ... TEMPLATE: a fresh schema in a fraction of a second rather
// than a server of its own.
package dbtest

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// The schema calls uuidv7(), which is PostgreSQL 18 or later.
const image = "postgres:18-alpine"

// initdbArgs puts every database of the container on PostgreSQL's builtin
// C.UTF-8 locale, which migrations/001_initial_schema.up.sql requires.
const initdbArgs = "--locale-provider=builtin --builtin-locale=C.UTF-8"

const (
	// templateName is the database the migrations are applied to. Nothing
	// connects to it afterwards: CREATE DATABASE refuses to copy a database
	// another session is connected to.
	templateName = "goen_template"
	// suiteName is the database Start returns.
	suiteName = "goen_test"
)

// shared is the server Start started for this test binary, which Pool and
// Database copy the template on.
var shared atomic.Pointer[server]

// Start brings up PostgreSQL, applies every migration to the template, and
// returns a pool on a copy of it and the teardown that removes the server.
// Pool and Database make their copies on the same server until the teardown
// runs, so TestMain calls Start once and the teardown after m.Run.
func Start(ctx context.Context) (*pgxpool.Pool, func(), error) {
	s, err := startServer(ctx)
	if err != nil {
		return nil, nil, err
	}
	pool, err := s.create(ctx, suiteName)
	if err != nil {
		s.stop()
		return nil, nil, err
	}
	shared.CompareAndSwap(nil, s)
	return pool, func() {
		pool.Close()
		shared.CompareAndSwap(s, nil)
		s.stop()
	}, nil
}

// Pool returns a pool on a database of the test's own, a copy of the migrated
// template, and drops the database when the test ends. The pool connects as
// the schema owner. Without a Start in TestMain, the test starts a server of
// its own, which is as isolated but takes seconds rather than milliseconds.
//
// Roles belong to the server, not to a database, so a test that creates one
// names it uniquely and drops it again.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	s := shared.Load()
	if s == nil {
		var err error
		if s, err = startServer(t.Context()); err != nil {
			t.Fatalf("start database: %v", err)
		}
		t.Cleanup(s.stop)
	}
	name := fmt.Sprintf("%s_%d", suiteName, s.copies.Add(1))
	pool, err := s.create(t.Context(), name)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if err := s.drop(context.WithoutCancel(t.Context()), name); err != nil {
			t.Errorf("drop database: %v", err)
		}
	})
	return pool
}

// Pools reaches one database as its owner and as each role the application
// runs as. cmd/goen logs in and assumes the role with SET ROLE; these pools
// assume it the same way, so a privilege the schema does not grant the role
// fails here as it would in production.
type Pools struct {
	Owner       *pgxpool.Pool // the schema owner, a superuser: fixtures and checks
	Store       *pgxpool.Pool // SET ROLE store: the storefront and its workers
	Admin       *pgxpool.Pool // SET ROLE admin: the back office
	Maintenance *pgxpool.Pool // SET ROLE maintenance: the background rebuilds
}

// Database is Pool with a pool per application role beside the owner's.
func Database(t *testing.T) Pools {
	t.Helper()
	owner := Pool(t)
	return Pools{
		Owner:       owner,
		Store:       RolePool(t, owner, "store"),
		Admin:       RolePool(t, owner, "admin"),
		Maintenance: RolePool(t, owner, "maintenance"),
	}
}

// RolePool returns a pool on owner's database whose every connection runs as
// role, and closes it when the test ends.
func RolePool(t *testing.T, owner *pgxpool.Pool, role string) *pgxpool.Pool {
	t.Helper()

	cfg := owner.Config()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
		return err
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open %s pool: %v", role, err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatalf("connect as %s: %v", role, err)
	}
	return pool
}

// server is one PostgreSQL container with the migrated template.
type server struct {
	url    url.URL       // reaches the template; a copy's URL differs only in its path
	admin  *pgxpool.Pool // on the maintenance database postgres, which creates and drops copies
	acl    databaseACL   // the template's privileges on itself
	stop   func()
	copies atomic.Int64
}

func startServer(ctx context.Context) (*server, error) {
	container, err := postgres.Run(ctx, image,
		postgres.WithDatabase(templateName),
		postgres.WithUsername("goen"),
		postgres.WithPassword("goen"),
		testcontainers.WithEnv(map[string]string{"POSTGRES_INITDB_ARGS": initdbArgs}),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("run postgres: %w", err)
	}
	terminate := func() {
		if terr := testcontainers.TerminateContainer(container); terr != nil {
			slog.Warn("dbtest: terminate postgres", "error", terr)
		}
	}

	// Seed scripts pass this DSN to libpq, which accepts custom settings through options.
	raw, err := container.ConnectionString(ctx, "sslmode=disable", "options=-c%20goen.catalogue_seed%3Ddevelopment")
	if err != nil {
		terminate()
		return nil, fmt.Errorf("connection string: %w", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		terminate()
		return nil, fmt.Errorf("parse connection string: %w", err)
	}
	s := &server{url: *u, stop: terminate}

	template, err := pgxpool.New(ctx, raw)
	if err != nil {
		terminate()
		return nil, fmt.Errorf("open pool: %w", err)
	}
	err = migrate(ctx, template)
	template.Close()
	if err != nil {
		terminate()
		return nil, err
	}

	if s.admin, err = pgxpool.New(ctx, s.urlOf("postgres")); err != nil {
		terminate()
		return nil, fmt.Errorf("open maintenance pool: %w", err)
	}
	s.stop = func() {
		s.admin.Close()
		terminate()
	}
	if s.acl, err = readDatabaseACL(ctx, s.admin, templateName); err != nil {
		s.stop()
		return nil, err
	}
	return s, nil
}

// urlOf is the server's connection string with database in place of the template.
func (s *server) urlOf(database string) string {
	u := s.url
	u.Path = database // String puts the slash between host and path
	return u.String()
}

// create copies the template to a new database called name and opens a pool on it.
func (s *server) create(ctx context.Context, name string) (*pgxpool.Pool, error) {
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := s.admin.Exec(ctx, "CREATE DATABASE "+quoted+" TEMPLATE "+pgx.Identifier{templateName}.Sanitize()); err != nil {
		return nil, fmt.Errorf("copy the template to %s: %w", name, err)
	}
	if replay := s.acl.on(quoted); replay != "" {
		if _, err := s.admin.Exec(ctx, replay); err != nil {
			return nil, fmt.Errorf("grant %s the template's database privileges: %w", name, err)
		}
	}
	pool, err := pgxpool.New(ctx, s.urlOf(name))
	if err != nil {
		return nil, fmt.Errorf("open pool on %s: %w", name, err)
	}
	return pool, nil
}

func (s *server) drop(ctx context.Context, name string) error {
	_, err := s.admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	return err
}

// databaseACL is what a database grants on itself beyond its owner's
// privileges. CREATE DATABASE copies everything inside a database but not the
// privileges on the database itself, so without replaying these a copy would
// hand PUBLIC the TEMPORARY privilege the schema revokes.
type databaseACL struct {
	explicit bool // false while the database keeps PostgreSQL's default
	grants   []databaseGrant
}

type databaseGrant struct {
	Privilege string // CONNECT, CREATE or TEMPORARY
	Grantee   string // PUBLIC, or a role name quoted as an identifier
	Grantable bool
}

func readDatabaseACL(ctx context.Context, admin *pgxpool.Pool, database string) (databaseACL, error) {
	var acl databaseACL
	if err := admin.QueryRow(ctx, `SELECT datacl IS NOT NULL FROM pg_database WHERE datname = $1`, database).Scan(&acl.explicit); err != nil {
		return acl, fmt.Errorf("read the template's database privileges: %w", err)
	}
	rows, err := admin.Query(ctx, `
		SELECT a.privilege_type,
		       CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE quote_ident(pg_get_userbyid(a.grantee)) END,
		       a.is_grantable
		FROM pg_database d CROSS JOIN LATERAL aclexplode(d.datacl) a
		WHERE d.datname = $1 AND a.grantee <> d.datdba
		ORDER BY 2, 1`, database)
	if err != nil {
		return acl, fmt.Errorf("read the template's database privileges: %w", err)
	}
	if acl.grants, err = pgx.CollectRows(rows, pgx.RowToStructByPos[databaseGrant]); err != nil {
		return acl, fmt.Errorf("read the template's database privileges: %w", err)
	}
	return acl, nil
}

// on returns the statements that give the database called quoted, an
// identifier already quoted, exactly these privileges, or "" when a new
// database has them already. The owner keeps its own, which the schema
// leaves alone.
func (acl databaseACL) on(quoted string) string {
	if !acl.explicit {
		return ""
	}
	statements := []string{"REVOKE ALL ON DATABASE " + quoted + " FROM PUBLIC"}
	for _, g := range acl.grants {
		statement := "GRANT " + g.Privilege + " ON DATABASE " + quoted + " TO " + g.Grantee
		if g.Grantable {
			statement += " WITH GRANT OPTION"
		}
		statements = append(statements, statement)
	}
	return strings.Join(statements, "; ")
}

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	dir, err := migrationsDir()
	if err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errNoMigrations
	}
	// The table golang-migrate keeps, as its postgres driver creates it, before
	// the first migration as it does: 001 revokes store's access to it, and the
	// binary refuses a database without its row.
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	// Glob returns sorted names; the numeric prefix is the applied order.
	var version int64
	for _, file := range files {
		sql, err := readFile(file)
		if err != nil {
			return err
		}
		if _, execErr := pool.Exec(ctx, sql); execErr != nil {
			return migrationError{file: filepath.Base(file), err: execErr}
		}
		prefix, _, _ := strings.Cut(filepath.Base(file), "_")
		if version, err = strconv.ParseInt(prefix, 10, 64); err != nil {
			return migrationError{file: filepath.Base(file), err: err}
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version, dirty) VALUES ($1, false)`, version); err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}
	return nil
}

func migrationsDir() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errNoCaller
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	return filepath.Join(root, "migrations"), nil
}
