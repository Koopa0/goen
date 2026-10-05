// Package pgtx ends pgx transactions the way every goen store needs: detached
// from the request that opened them, and bounded in time.
package pgtx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// rollbackTimeout bounds the ROLLBACK's one round trip. Past it pgx closes the
// connection instead, which ends the transaction on the server just as surely,
// so waiting longer only holds the caller and its pooled connection.
const rollbackTimeout = 5 * time.Second

// Rollback ends tx unless it has already committed, and is meant to be
// deferred right after Begin.
//
// It keeps ctx's values but not its cancellation: pgx fails a ROLLBACK whose
// context is done before sending it and destroys the connection, so a request
// whose client left would roll nothing back.
func Rollback(ctx context.Context, tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	// After a commit this is pgx.ErrTxClosed; any other failure has already made
	// pgx close the connection, which aborts the transaction.
	_ = tx.Rollback(ctx) //nolint:errcheck // nothing a caller can do differs by the result
}
