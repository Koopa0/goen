// Package db is the sqlc-generated query layer, with HasConstraint to read the
// errors those queries return.
package db

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// HasConstraint reports whether err is a violation of this constraint. Bound to
// ConstraintName: a PgError's message never contains it, so a substring search
// cannot match.
func HasConstraint(err error, constraint string) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.ConstraintName == constraint
}
