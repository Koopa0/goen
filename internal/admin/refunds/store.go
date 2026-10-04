// Package refunds pays money back: an approved return's payout through the card
// and the store-credit ledger, its recovery when a source did not settle, and the
// refund before shipment that cancels a paid order nothing has left.
package refunds

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/invoice"
)

var (
	ErrNotFound = errors.New("refunds: not found")
	ErrInvalid  = errors.New("refunds: invalid input")
	// ErrUnsettled is a refund before shipment whose card refund Stripe
	// accepted and has not settled; the order stays open until a resume sees it land.
	ErrUnsettled = errors.New("refunds: the refund is recorded and has not settled")
)

type Store struct {
	pool     *pgxpool.Pool
	q        *db.Queries
	refunder Refunder
	// invoices corrects the 統一發票 of an order refunded before shipment. Nil
	// leaves that to staff, for a store that never cancels an order.
	invoices *invoice.Store
}

// NewStore returns a Store over the admin pool. refunder may be one that
// refuses: a back office without Stripe credentials can still decide returns,
// and a refund it cannot pay must fail loudly.
func NewStore(pool *pgxpool.Pool, refunder Refunder, invoices *invoice.Store) *Store {
	if pool == nil || refunder == nil {
		panic("refunds: NewStore requires a pool and a refunder")
	}
	return &Store{pool: pool, q: db.New(pool), refunder: refunder, invoices: invoices}
}
