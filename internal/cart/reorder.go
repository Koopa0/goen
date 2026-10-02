package cart

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

type Reorder struct {
	Added    int
	Adjusted bool
	Skipped  []SkippedLine
}

type SkippedLine struct {
	Name   string
	Label  string
	Reason SkipReason
}

type SkipReason string

const (
	SkipGone    SkipReason = "gone"
	SkipSoldOut SkipReason = "sold_out"
)

// Reorder prices lines at today's prices, not the order's.
func (s *Store) Reorder(ctx context.Context, cartID uuid.UUID, number string) (Reorder, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Reorder{}, fmt.Errorf("begin reorder: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)
	if lockErr := lockCart(ctx, q, cartID); lockErr != nil {
		return Reorder{}, lockErr
	}

	lines, err := q.ReorderLines(ctx, number)
	if err != nil {
		return Reorder{}, fmt.Errorf("read order %s for reorder: %w", number, err)
	}

	var out Reorder
	for i := range lines {
		l := &lines[i]
		skipped := SkippedLine{Name: l.ProductName, Label: l.VariantLabel.String}

		switch {
		case !l.VariantID.Valid || !l.Sellable:
			skipped.Reason = SkipGone
			out.Skipped = append(out.Skipped, skipped)
			continue
		case l.Available <= 0:
			skipped.Reason = SkipSoldOut
			out.Skipped = append(out.Skipped, skipped)
			continue
		}

		adjusted, addErr := addCartItem(ctx, q, cartID, l.VariantID.UUID, l.Quantity)
		if addErr != nil {
			// Any revalidation or write failure for a selected line aborts the
			// whole reorder.
			return Reorder{}, fmt.Errorf("add %s to cart: %w", l.ProductName, addErr)
		}
		out.Adjusted = out.Adjusted || adjusted
		out.Added++
	}
	if err := tx.Commit(ctx); err != nil {
		return Reorder{}, fmt.Errorf("commit reorder: %w", err)
	}
	return out, nil
}
