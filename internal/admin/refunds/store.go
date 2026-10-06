// Package refunds pays money back: an approved return's payout through the card
// and the store-credit ledger, its recovery when a source did not settle, and the
// refund before shipment that cancels a paid order nothing has left.
package refunds

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/refundstate"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/invoice"
)

var (
	ErrNotFound = errors.New("refunds: not found")
	ErrInvalid  = errors.New("refunds: invalid input")
	// ErrUnsettled is a refund before shipment whose card refund Stripe
	// accepted and has not settled; the order stays open until a resume sees it land.
	ErrUnsettled = errors.New("refunds: the refund is recorded and has not settled")
	// ErrCancellationIncomplete means the money, refunded event and points
	// settled; Resume must finish cancellation without another payout.
	ErrCancellationIncomplete = fmt.Errorf("%w: the order cancellation did not complete", refundstate.ErrIncomplete)

	// The refusals the order's own state decides, before any money moves.
	ErrShipped        = fmt.Errorf("%w: the order has shipped", refundstate.ErrRefused)
	ErrHasReturn      = fmt.Errorf("%w: the order already has a return", refundstate.ErrRefused)
	ErrOrderCancelled = fmt.Errorf("%w: the order is cancelled", refundstate.ErrRefused)
	ErrNotPaid        = fmt.Errorf("%w: the order is not paid", refundstate.ErrRefused)
	// ErrPayoutUnfit is a return whose recorded split, settled amounts or
	// owner no longer allow its payout.
	ErrPayoutUnfit = fmt.Errorf("%w: the payout does not fit the return", refundstate.ErrRefused)
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
