// Package pgerr reads the errors PostgreSQL returns, by what the server names
// and never by the message text.
package pgerr

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// IsConstraint reports whether err is a violation of this constraint. Bound to
// ConstraintName: a PgError's message never contains it, so a substring search
// cannot match.
func IsConstraint(err error, constraint string) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.ConstraintName == constraint
}

// WrapRefusal returns err wrapped in refused when it is PostgreSQL refusing a
// write by one of the schema's rules, and err unchanged otherwise, so a desk's
// sentinel marks exactly what a rule decided. A refusal is an integrity
// constraint violation or an error naming a constraint, which is how every
// trigger in migrations/001 raises; a timeout, a deadlock or a lost connection
// is none.
func WrapRefusal(err, refused error) error {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || (pgErr.ConstraintName == "" && !strings.HasPrefix(pgErr.Code, "23")) {
		return err
	}
	return fmt.Errorf("%w: %w", refused, err)
}
