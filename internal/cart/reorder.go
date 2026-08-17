package cart

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Reorder is the outcome of putting a past order back in the cart. It reports
// what was skipped as well as what was added, because a reorder that quietly
// drops two of five lines is a customer checking out with the wrong basket.
type Reorder struct {
	Added   int
	Skipped []SkippedLine
}

// SkippedLine is one thing the reorder could not put back, and why.
type SkippedLine struct {
	Name   string
	Label  string
	Reason SkipReason
}

// SkipReason is why a line could not be reordered.
type SkipReason string

const (
	// SkipGone is a variant that no longer exists or is no longer for sale.
	SkipGone SkipReason = "gone"
	// SkipSoldOut is a variant that exists and has nothing on the shelf.
	SkipSoldOut SkipReason = "sold_out"
)

// Reorder puts every still-sellable line of a past order back in the cart, at
// today's prices rather than the order's: a reorder is a new purchase, and
// quoting last year's figure is quoting one the checkout will not honour.
func (s *Store) Reorder(ctx context.Context, cartID uuid.UUID, number string) (Reorder, error) {
	lines, err := s.q.ReorderLines(ctx, number)
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

		if err := s.Add(ctx, cartID, l.VariantID.UUID, l.Quantity); err != nil {
			// Add already clamps what is short, so an error here is the write
			// failing rather than "out of stock", and must not be skipped.
			return Reorder{}, fmt.Errorf("add %s to cart: %w", l.ProductName, err)
		}
		out.Added++
	}
	return out, nil
}
