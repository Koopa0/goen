// Package migrations holds goen's schema migrations, which golang-migrate
// applies, and tells whether a database was built from them.
//
// A database reports its schema through schema_version(), which
// 001_initial_schema.up.sql creates: the digest of the 001 it was built from,
// and the version golang-migrate recorded. 001 was amended in place until it
// was frozen, so the version alone cannot tell a database built from an earlier
// edition of it from one built from the frozen file; the digest can. Every
// later change is a new migration, and raises the version.
package migrations

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed *.up.sql
var files embed.FS

// initial is the migration whose digest a database records.
const initial = "001_initial_schema.up.sql"

// digestLine is the line of initial that records its digest. The digest is
// computed with those 64 hex digits set to zeros, since a file cannot hold its
// own hash, as an RFC 791 header checksum is computed with its own field zero.
var digestLine = regexp.MustCompile(`(?m)^ {4}digest := '([0-9a-f]{64})';$`)

// Schema is what schema_version() reports for a database built from these
// migrations.
type Schema struct {
	Digest  string // SHA-256 of 001_initial_schema.up.sql with its digest zeroed, in hex
	Version int64  // the highest migration number, as golang-migrate records it
}

// Built returns the schema of the migrations this binary was built with.
func Built() (Schema, error) { return built() }

var built = sync.OnceValues(func() (Schema, error) {
	src, err := files.ReadFile(initial)
	if err != nil {
		return Schema{}, err
	}
	digest, _, err := Digest(src)
	if err != nil {
		return Schema{}, err
	}
	names, err := fs.Glob(files, "*.up.sql")
	if err != nil {
		return Schema{}, err
	}
	s := Schema{Digest: digest}
	for _, name := range names {
		prefix, _, _ := strings.Cut(name, "_")
		n, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			return Schema{}, fmt.Errorf("migration %s: %w", name, err)
		}
		s.Version = max(s.Version, n)
	}
	return s, nil
})

// Digest returns the digest of src, the text of 001_initial_schema.up.sql,
// and the digest src itself records.
func Digest(src []byte) (computed, recorded string, err error) {
	at := digestLine.FindAllSubmatchIndex(src, -1)
	if len(at) != 1 {
		return "", "", fmt.Errorf("%s records its digest on %d lines, want 1", initial, len(at))
	}
	start, end := at[0][2], at[0][3]
	zeroed := make([]byte, 0, len(src))
	zeroed = append(zeroed, src[:start]...)
	zeroed = append(zeroed, strings.Repeat("0", end-start)...)
	zeroed = append(zeroed, src[end:]...)
	sum := sha256.Sum256(zeroed)
	return hex.EncodeToString(sum[:]), string(src[start:end]), nil
}

// Querier runs one query; *pgxpool.Pool and pgx.Tx satisfy it.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Check returns an error unless the database q reaches has schema s: the same
// 001, a version no older than s's, and no migration left half-applied. A newer
// version is accepted, because a migration is applied before the binary that
// needs it is deployed.
func (s Schema) Check(ctx context.Context, q Querier) error {
	var digest *string
	var version *int64
	var dirty *bool
	err := q.QueryRow(ctx, `SELECT digest, version, dirty FROM schema_version()`).Scan(&digest, &version, &dirty)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "42883" {
		return errors.New("the database has no schema_version(): it was built from an older migrations/001")
	}
	if err != nil {
		return fmt.Errorf("read the database's schema version: %w", err)
	}
	switch {
	case digest == nil || *digest != s.Digest:
		return fmt.Errorf("the database was built from a different migrations/001 (digest %s, want %s)", deref(digest), s.Digest)
	case version == nil || dirty == nil:
		return errors.New("the database has no golang-migrate schema_migrations row")
	case *dirty:
		return fmt.Errorf("migration %d did not finish (schema_migrations.dirty)", *version)
	case *version < s.Version:
		return fmt.Errorf("the database is at migration %d, want %d", *version, s.Version)
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return "none"
	}
	return *s
}
