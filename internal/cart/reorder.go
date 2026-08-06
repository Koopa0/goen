package cart

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Reorder is what putting a past order back in the cart came to.
//
// It reports what was SKIPPED as well as what was added, because a reorder that
// quietly drops two of five lines is a customer who checks out with the wrong
// basket. The names are what was bought — order_lines keeps them — so a line
// whose variant has since been deleted can still be named.
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

// The reasons a line is skipped. Separate values rather than one message,
// because the customer's next step differs: gone means find something else,
// sold out means come back.
const (
	// SkipGone is a variant that no longer exists or is no longer for sale.
	SkipGone SkipReason = "gone"
	// SkipSoldOut is a variant that exists and has nothing on the shelf.
	SkipSoldOut SkipReason = "sold_out"
)

// Reorder puts every still-sellable line of a past order back in the cart.
//
// At TODAY's prices, not the order's. A reorder is a new purchase and the cart
// prices itself from the catalogue; showing last year's price would be quoting
// a figure the checkout will not honour — the same rule the cart already
// follows for a price that moved while something sat in it.
//
// Quantities are clamped to what can be supplied rather than refused, which is
// what Add already does for a direct add: a customer who bought three and can
// have one should get one and be told, not nothing and a message.
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
			// Add refuses what cannot be sold at all and clamps what is short,
			// so an error here is not "out of stock" — it is the write failing,
			// and silently skipping it would tell the customer their basket is
			// what they asked for.
			return Reorder{}, fmt.Errorf("add %s to cart: %w", l.ProductName, err)
		}
		out.Added++
	}
	return out, nil
}
