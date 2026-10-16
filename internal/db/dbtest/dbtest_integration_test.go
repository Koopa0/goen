//go:build integration

package dbtest_test

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
)

// databaseRow is what pg_database and pg_db_role_setting hold about a database
// itself, outside the database: the part CREATE DATABASE ... TEMPLATE does not
// copy, plus the locale it does.
type databaseRow struct {
	Owner, Encoding, LocaleProvider, Locale, Collate, Ctype string
	ConnectionLimit                                         int32
	AllowConnections                                        bool
	ACL                                                     []string // grantor>grantee:privilege, * when grantable
	Settings                                                []string // per-database settings, per role
}

const databaseRowSQL = `
SELECT pg_get_userbyid(d.datdba),
       pg_encoding_to_char(d.encoding),
       d.datlocprovider::text,
       coalesce(d.datlocale, ''),
       d.datcollate,
       d.datctype,
       d.datconnlimit,
       d.datallowconn,
       coalesce((SELECT array_agg(entry ORDER BY entry)
                 FROM (SELECT format('%s>%s:%s%s',
                                     pg_get_userbyid(a.grantor),
                                     CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(a.grantee) END,
                                     a.privilege_type,
                                     CASE WHEN a.is_grantable THEN '*' ELSE '' END) AS entry
                       FROM aclexplode(coalesce(d.datacl, acldefault('d', d.datdba))) a) acl), '{}'),
       coalesce((SELECT array_agg(entry ORDER BY entry)
                 FROM (SELECT format('%s=%s', s.setrole::regrole, array_to_string(s.setconfig, ',')) AS entry
                       FROM pg_db_role_setting s WHERE s.setdatabase = d.oid) settings), '{}')
FROM pg_database d
WHERE d.datname = $1`

func readDatabaseRow(t *testing.T, pool *pgxpool.Pool, name string) databaseRow {
	t.Helper()
	rows, err := pool.Query(t.Context(), databaseRowSQL, name)
	if err != nil {
		t.Fatalf("read database %s: %v", name, err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[databaseRow])
	if err != nil {
		t.Fatalf("read database %s: %v", name, err)
	}
	return row
}

// TestPoolCopiesDatabasePrivileges: the database a test gets must match the
// one the migrations ran on. CREATE DATABASE ... TEMPLATE copies the schema but
// not the privileges on the database itself, and the schema revokes one of
// those: TEMPORARY from PUBLIC, so that store cannot plant a pg_temp table that
// shadows a guard's.
func TestPoolCopiesDatabasePrivileges(t *testing.T) {
	pool := dbtest.Pool(t)
	copied := pool.Config().ConnConfig.Database

	template := readDatabaseRow(t, pool, dbtest.TemplateName)
	if slices.Contains(template.ACL, "goen>PUBLIC:TEMPORARY") {
		t.Fatalf("template %s ACL = %v, want PUBLIC without TEMPORARY: a copy with the default would then match it and the comparison prove nothing",
			dbtest.TemplateName, template.ACL)
	}
	if diff := cmp.Diff(template, readDatabaseRow(t, pool, copied)); diff != "" {
		t.Errorf("database %s differs from its template %s (-template +copy):\n%s", copied, dbtest.TemplateName, diff)
	}
}

// TestDatabaseAssumesEachRole: a role pool that silently stayed the owner, a
// superuser, would pass every privilege the schema withholds.
func TestDatabaseAssumesEachRole(t *testing.T) {
	db := dbtest.Database(t)
	want := db.Owner.Config().ConnConfig.Database
	for role, pool := range map[string]*pgxpool.Pool{
		"goen":        db.Owner,
		"store":       db.Store,
		"admin":       db.Admin,
		"maintenance": db.Maintenance,
	} {
		var user, database string
		var superuser bool
		if err := pool.QueryRow(t.Context(),
			`SELECT current_user, current_database(), current_setting('is_superuser')::boolean`,
		).Scan(&user, &database, &superuser); err != nil {
			t.Fatalf("%s pool: %v", role, err)
		}
		if user != role || database != want || superuser != (role == "goen") {
			t.Errorf("%s pool runs as %s in %s, superuser %t; want %s in %s, superuser %t",
				role, user, database, superuser, role, want, role == "goen")
		}
	}
}
