package migrations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// frozen is the SHA-256 of every file golang-migrate reads from this
// directory (the names its source.Regex matches). golang-migrate records only
// the version a database has reached, so an edit to a migration never reaches a
// database already past it: a merged migration is never edited, and a change is
// a new migration. A down file stays the inverse of the up file it merged with.
// A new migration's files join this map in the pull request that adds them.
var frozen = map[string]string{
	"001_initial_schema.down.sql": "0a9d734ff6ce19a006bde8c2b307831e4746ce050473383ce8746881874da70f",
	"001_initial_schema.up.sql":   "164fb800a883b36fd45f847102da04a6a6d6abf58421c3c24c71a43352be73cf",
}

// migrationName is golang-migrate's source.Regex, which names the files it
// applies.
var migrationName = regexp.MustCompile(`^(\d+)_(.*)\.(down|up)\.(.*)$`)

// TestFrozenMigrationsAreUnchanged: no migration changes once it has merged,
// and none is added or removed without frozen saying so.
func TestFrozenMigrationsAreUnchanged(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	found := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !migrationName.MatchString(e.Name()) {
			continue
		}
		found[e.Name()] = true
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(src)
		got := hex.EncodeToString(sum[:])
		want, ok := frozen[e.Name()]
		switch {
		case !ok:
			t.Errorf("%s is not in frozen: add it, with SHA-256 %s, in the pull request that adds the migration", e.Name(), got)
		case got != want:
			t.Errorf("%s has SHA-256 %s, want %s: a merged migration is never edited; make the change a new migration (CONTRIBUTING.md, Migrations)", e.Name(), got, want)
		}
	}
	for name := range frozen {
		if !found[name] {
			t.Errorf("%s is in frozen but not in migrations/: a merged migration is never removed", name)
		}
	}
}

// TestInitialRecordsItsDigest: the digest 001 records is the one Digest
// computes from it, so a database built from 001 passes the binary's check.
func TestInitialRecordsItsDigest(t *testing.T) {
	src, err := os.ReadFile(initial)
	if err != nil {
		t.Fatal(err)
	}
	computed, recorded, err := Digest(src)
	if err != nil {
		t.Fatal(err)
	}
	if computed != recorded {
		t.Errorf("Digest(%s) = %s, but the file records %s: every database built from it would be refused", initial, computed, recorded)
	}
}

// TestDigestSkipsOnlyItsOwnField: rewriting the recorded digest leaves the
// computed one alone, and any other change moves it.
func TestDigestSkipsOnlyItsOwnField(t *testing.T) {
	const line = "    digest := '%s';\n"
	src := []byte("SELECT 1;\n" + strings.ReplaceAll(line, "%s", strings.Repeat("a", 64)) + "SELECT 2;\n")
	base, recorded, err := Digest(src)
	if err != nil {
		t.Fatal(err)
	}
	if recorded != strings.Repeat("a", 64) {
		t.Fatalf("recorded = %q, want the 64 a's", recorded)
	}
	other := bytes.Replace(src, []byte(recorded), []byte(strings.Repeat("b", 64)), 1)
	if got, _, _ := Digest(other); got != base {
		t.Errorf("changing the recorded digest moved the computed one: %s, want %s", got, base)
	}
	edited := bytes.Replace(src, []byte("SELECT 2"), []byte("SELECT 3"), 1)
	if got, _, _ := Digest(edited); got == base {
		t.Error("an edit outside the digest line left the digest unchanged")
	}
	if _, _, err := Digest(append(src, src...)); err == nil {
		t.Error("two digest lines were accepted; the digest would zero only one of them")
	}
}

type row struct {
	digest  *string
	version *int64
	dirty   *bool
	err     error
}

func (r row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(**string) = r.digest
	*dest[1].(**int64) = r.version
	*dest[2].(**bool) = r.dirty
	return nil
}

type querier struct{ row row }

func (q querier) QueryRow(context.Context, string, ...any) pgx.Row { return q.row }

func ptr[T any](v T) *T { return &v }

// TestCheckRefusesEveryOtherSchema is the gate /readyz and startup apply.
func TestCheckRefusesEveryOtherSchema(t *testing.T) {
	want := Schema{Digest: strings.Repeat("1", 64), Version: 2}
	for _, tc := range []struct {
		name    string
		row     row
		wantErr string // "" accepts
	}{
		{"the same schema", row{digest: ptr(want.Digest), version: ptr[int64](2), dirty: ptr(false)}, ""},
		{"a newer migration", row{digest: ptr(want.Digest), version: ptr[int64](3), dirty: ptr(false)}, ""},
		{"one migration behind", row{digest: ptr(want.Digest), version: ptr[int64](1), dirty: ptr(false)}, "at migration 1, want 2"},
		{"another edition of 001", row{digest: ptr(strings.Repeat("2", 64)), version: ptr[int64](2), dirty: ptr(false)}, "different migrations/001"},
		{"a half-applied migration", row{digest: ptr(want.Digest), version: ptr[int64](2), dirty: ptr(true)}, "did not finish"},
		{"not built by golang-migrate", row{digest: ptr(want.Digest)}, "no golang-migrate schema_migrations row"},
		{"older than schema_version()", row{err: &pgconn.PgError{Code: "42883"}}, "no schema_version()"},
		{"unreachable", row{err: errors.New("connection refused")}, "connection refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := want.Check(t.Context(), querier{tc.row})
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("Check = %v, want nil", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("Check = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

// TestBuiltIsTheEmbeddedMigrations: the binary's expectation is read from the
// files it embeds, not from a constant that could fall behind them.
func TestBuiltIsTheEmbeddedMigrations(t *testing.T) {
	s, err := Built()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(initial)
	if err != nil {
		t.Fatal(err)
	}
	computed, _, err := Digest(src)
	if err != nil {
		t.Fatal(err)
	}
	names, err := filepath.Glob("*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	last, _, _ := strings.Cut(names[len(names)-1], "_")
	if s.Digest != computed || strconv.FormatInt(s.Version, 10) != strings.TrimLeft(last, "0") {
		t.Errorf("Built = %+v, want digest %s and version %s", s, computed, last)
	}
}
