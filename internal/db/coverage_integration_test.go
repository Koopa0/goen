//go:build integration

package db_test

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// This file exists because the previous suite lied.
//
// It reported 48 green subtests and was read as "every constraint is
// exercised". Deleting constraints one at a time showed that 48 of the 65
// named CHECKs could be removed without a single test turning red: the suite
// asserted the rules it happened to think of, and its silence about the rest
// looked exactly like coverage.
//
// The fix is to stop trusting a hand-written list. Every test below derives
// its expectations from the live catalog, so a constraint added to the
// migration without a case here fails the build rather than quietly joining
// the untested majority.

// TestEveryCheckConstraintIsExercised is the completeness gate. It reads the
// constraint names PostgreSQL actually created and requires a case for each.
func TestEveryCheckConstraintIsExercised(t *testing.T) {
	live := liveCheckConstraints(t)

	covered := make(map[string]bool, len(checkCases))
	for _, c := range checkCases {
		covered[c.constraint] = true
	}

	var missing []string
	for _, name := range live {
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d CHECK constraints have no case in checkCases:\n  %s\n\n"+
			"Add one per constraint. A constraint with no case is a constraint\n"+
			"nobody has watched reject anything.",
			len(missing), strings.Join(missing, "\n  "))
	}

	// The reverse direction: a case naming a constraint that no longer exists
	// is a test that silently stopped testing when the schema moved on.
	inLive := make(map[string]bool, len(live))
	for _, name := range live {
		inLive[name] = true
	}
	var stale []string
	for _, c := range checkCases {
		if !inLive[c.constraint] {
			stale = append(stale, c.constraint)
		}
	}
	if len(stale) > 0 {
		t.Errorf("%d cases name a constraint the database does not have:\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// TestCheckConstraintsReject runs every case's rejecting statement and requires
// that the named constraint — not merely some constraint — is what refused it.
//
// Binding the assertion to the constraint name is the point. The old suite
// accepted any error, so a statement that tripped an unrelated unique index
// counted as proof that the CHECK worked.
func TestCheckConstraintsReject(t *testing.T) {
	for _, c := range checkCases {
		t.Run(c.constraint, func(t *testing.T) {
			err := run(t, c.reject)
			if err == nil {
				t.Fatalf("the database accepted the row; %s does not enforce this", c.constraint)
			}

			code, name := constraintViolation(err)
			if code != "23514" {
				t.Fatalf("refused with SQLSTATE %s (constraint %q), want a 23514 check violation: %v",
					code, name, err)
			}
			if name != c.constraint {
				t.Fatalf("refused by %q, want %q — this case is proving the wrong rule",
					name, c.constraint)
			}
		})
	}
}

// TestCheckConstraintsAccept runs the neighbouring legal value. Without it a
// constraint that refuses everything is indistinguishable from a correct one.
func TestCheckConstraintsAccept(t *testing.T) {
	for _, c := range checkCases {
		if c.accept == "" {
			t.Run(c.constraint, func(t *testing.T) {
				t.Skipf("no legal neighbour: %s", c.acceptNote)
			})
			continue
		}
		t.Run(c.constraint, func(t *testing.T) {
			if err := run(t, c.accept); err != nil {
				t.Fatalf("the database refused a legal row: %v", err)
			}
		})
	}
}

// TestEveryUniqueConstraintIsExercised applies the same completeness rule to
// the unique indexes that carry a business rule.
func TestEveryUniqueConstraintIsExercised(t *testing.T) {
	live := liveUniqueIndexes(t)

	covered := make(map[string]bool, len(uniqueCases))
	for _, c := range uniqueCases {
		covered[c.index] = true
	}

	var missing []string
	for _, name := range live {
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d unique indexes have no case in uniqueCases:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// TestUniqueConstraintsReject requires each unique index to refuse its own
// duplicate, identified by name.
func TestUniqueConstraintsReject(t *testing.T) {
	for _, c := range uniqueCases {
		t.Run(c.index, func(t *testing.T) {
			err := run(t, c.reject)
			if err == nil {
				t.Fatalf("the database accepted the duplicate; %s does not enforce this", c.index)
			}
			code, name := constraintViolation(err)
			if code != "23505" {
				t.Fatalf("refused with SQLSTATE %s (constraint %q), want a 23505 unique violation: %v",
					code, name, err)
			}
			if name != c.index {
				t.Fatalf("refused by %q, want %q", name, c.index)
			}
		})
	}
}

// TestUniqueConstraintsAdmitTheNeighbour proves each index is scoped as
// intended: the near-duplicate that differs in the one dimension the index
// does not cover must be accepted.
func TestUniqueConstraintsAdmitTheNeighbour(t *testing.T) {
	for _, c := range uniqueCases {
		if c.accept == "" {
			t.Run(c.index, func(t *testing.T) {
				t.Skipf("no meaningful neighbour: %s", c.acceptNote)
			})
			continue
		}
		t.Run(c.index, func(t *testing.T) {
			if err := run(t, c.accept); err != nil {
				t.Fatalf("the database refused a legal near-duplicate: %v", err)
			}
		})
	}
}

// liveCheckConstraints returns every named CHECK in the public schema.
//
// NOT NULL is excluded: PostgreSQL 18 records those as CHECK constraints with
// generated names, and they are covered by the column definitions themselves.
func liveCheckConstraints(t *testing.T) []string {
	t.Helper()

	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT c.conname
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		WHERE c.contype = 'c'
		  AND c.connamespace = 'public'::regnamespace
		  AND t.relname <> 'schema_migrations'
		  AND NOT c.conname LIKE '%_not_null'
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("read check constraints: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no check constraints found; the migration did not apply")
	}
	sort.Strings(names)
	return names
}

// liveUniqueIndexes returns the unique indexes that encode a business rule.
// Primary keys are excluded: uniqueness of a generated surrogate key is a
// property of uuidv7, not a rule about goen.
func liveUniqueIndexes(t *testing.T) []string {
	t.Helper()

	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT i.relname
		FROM pg_index x
		JOIN pg_class i ON i.oid = x.indexrelid
		JOIN pg_class t ON t.oid = x.indrelid
		WHERE x.indisunique
		  AND NOT x.indisprimary
		  AND i.relnamespace = 'public'::regnamespace
		  AND t.relname <> 'schema_migrations'
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("read unique indexes: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	sort.Strings(names)
	return names
}

// checkCase pairs one CHECK constraint with a statement it must refuse and,
// where one exists, the neighbouring statement it must accept.
type checkCase struct {
	constraint string
	reject     string
	accept     string
	// acceptNote explains why no accepting statement exists, for the few
	// constraints whose legal side is already covered by the fixtures.
	acceptNote string
}

// uniqueCase pairs one unique index with a duplicate it must refuse and a
// near-duplicate it must admit.
type uniqueCase struct {
	index      string
	reject     string
	accept     string
	acceptNote string
}

// constraintViolation extracts the SQLSTATE and constraint name PostgreSQL
// reported, so an assertion can name the rule it is proving instead of
// accepting any failure at all. That distinction is what the previous suite
// lacked: a statement tripping an unrelated unique index read as proof that
// the CHECK under test worked.
func constraintViolation(err error) (code, constraint string) {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return "", ""
	}
	return pgErr.Code, pgErr.ConstraintName
}
