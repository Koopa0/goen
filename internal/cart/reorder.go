package cart

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
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
	// Slug is empty once the product is no longer listed, so there is no page to link.
	Slug string
}

type SkipReason string

const (
	SkipGone    SkipReason = "gone"
	SkipSoldOut SkipReason = "sold_out"
)

// skipReason is why a line of a past order cannot go back in a cart, and empty
// when it can.
func skipReason(l *db.ReorderLinesRow) SkipReason {
	switch {
	case !l.VariantID.Valid || !l.Sellable:
		return SkipGone
	case l.Available <= 0:
		return SkipSoldOut
	default:
		return ""
	}
}

func skippedLine(l *db.ReorderLinesRow, reason SkipReason) SkippedLine {
	s := SkippedLine{Name: l.ProductName, Label: l.VariantLabel.String, Reason: reason}
	if l.Listed {
		s.Slug = l.ProductSlug
	}
	return s
}

// SkippedOnReorder is what buying the order again would leave out as of now. It
// reads the order and touches no cart.
func (s *Store) SkippedOnReorder(ctx context.Context, number string) ([]SkippedLine, error) {
	lines, err := s.q.ReorderLines(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("read order %s for reorder: %w", number, err)
	}
	var out []SkippedLine
	for i := range lines {
		if reason := skipReason(&lines[i]); reason != "" {
			out = append(out, skippedLine(&lines[i], reason))
		}
	}
	return out, nil
}

// Reorder prices lines at today's prices, not the order's.
func (s *Store) Reorder(ctx context.Context, cartID uuid.UUID, number string) (Reorder, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Reorder{}, fmt.Errorf("begin reorder: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	if lockErr := Lock(ctx, q, cartID); lockErr != nil {
		return Reorder{}, lockErr
	}

	lines, err := q.ReorderLines(ctx, number)
	if err != nil {
		return Reorder{}, fmt.Errorf("read order %s for reorder: %w", number, err)
	}

	var out Reorder
	for i := range lines {
		l := &lines[i]
		if reason := skipReason(l); reason != "" {
			out.Skipped = append(out.Skipped, skippedLine(l, reason))
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
