// Package pgerr reads the errors PostgreSQL returns, by what the server names
// and never by the message text.
package pgerr

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// IsConstraint reports whether err is a violation of this constraint. Bound to
// ConstraintName: a PgError's message never contains it, so a substring search
// cannot match.
func IsConstraint(err error, constraint string) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.ConstraintName == constraint
}
