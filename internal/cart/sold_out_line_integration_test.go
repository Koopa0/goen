//go:build integration

package cart_test

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/orderaccess"
)

// A sold-out line stays in the cart, is left out of the count and the subtotal,
// and keeps the shopper out of the checkout until it is removed.
func TestASoldOutLineIsNotCountedAndBlocksCheckout(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, nil)
	cartID, token := newCartSession(t, s)

	kept, gone, _ := variantsOf(t, "soldout-line", 2)
	if err := s.Add(ctx, cartID, kept, 2); err != nil {
		t.Fatalf("add the line that stays: %v", err)
	}
	if err := s.Add(ctx, cartID, gone, 1); err != nil {
		t.Fatalf("add the line that sells out: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET stock_quantity = safety_stock WHERE id = $1`, gone); err != nil {
		t.Fatalf("sell out: %v", err)
	}

	view, err := s.View(ctx, cartID)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	var soldOut, keptUnitCents int64
	for _, l := range view.Lines {
		switch l.VariantID {
		case gone.String():
			if !l.Unavailable {
				t.Errorf("the sold-out line is not Unavailable: %+v", l)
			}
			soldOut++
		case kept.String():
			keptUnitCents = l.UnitCents
		}
	}
	if soldOut != 1 || len(view.Lines) != 2 {
		t.Fatalf("the cart holds %d lines with %d sold out, want 2 and 1", len(view.Lines), soldOut)
	}
	if view.ItemCount != 2 {
		t.Errorf("item count = %d, want 2: a sold-out line is not counted", view.ItemCount)
	}
	if want := keptUnitCents * 2; view.SubtotalCents != want {
		t.Errorf("subtotal = %d, want %d: a sold-out line is not priced", view.SubtotalCents, want)
	}

	_, status := openTheCheckout(t, h, token, "")
	if status != http.StatusSeeOther {
		t.Errorf("GET /checkout with a sold-out line answered %d, want 303 back to the cart", status)
	}
}
