//go:build integration

package checkout_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/order/delivery"
)

// TestCheckoutAndReorderSerializeTheCartAggregate fixes the lost-update window
// between checkout's cart snapshot and ClearCart. Reorder must wait for the
// checkout transaction, then add to the now-empty cart; otherwise it can report
// success only for ClearCart to erase items that were never copied to the order.
func TestCheckoutAndReorderSerializeTheCartAggregate(t *testing.T) {
	ctx := t.Context()
	baseStore := checkout.NewStore(pool)
	checkoutVariant, reorderedVariant, _ := threeVariants(t, "cart-aggregate-lock")
	sourceOrder := orderOfVariants(t, map[uuid.UUID]int32{reorderedVariant: 2})
	basket := newCart(t, baseStore)
	if err := baseStore.Carts().Add(ctx, basket, checkoutVariant, 1); err != nil {
		t.Fatalf("add checkout line: %v", err)
	}

	// Hold the checkout's first stock root. The fixed checkout has already
	// locked the cart aggregate before it reaches this wait.
	variantTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin variant blocker: %v", err)
	}
	defer func() { _ = variantTx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := variantTx.Exec(ctx,
		`SELECT 1 FROM product_variants WHERE id = $1 FOR UPDATE`, checkoutVariant); err != nil {
		t.Fatalf("lock checkout variant: %v", err)
	}

	checkoutPool := applicationPool(t, "checkout-cart-aggregate")
	reorderPool := applicationPool(t, "reorder-cart-aggregate")
	shippingVersionID := shipVersionFor(t, "home_delivery")
	checkoutStore := checkout.NewStore(checkoutPool)
	checkoutAddress := &delivery.Details{
		Email: "aggregate@example.com", RecipientName: "購物車鎖", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	shown := checkoutQuote(
		t, checkoutStore, basket, uuid.NullUUID{}, shippingVersionID, checkoutAddress, "",
	)
	checkoutDone := make(chan error, 1)
	checkoutNumber := make(chan string, 1)
	go func() {
		number, placeErr := checkoutStore.PlaceOrder(
			ctx,
			basket,
			uuid.NullUUID{},
			shippingVersionID,
			checkoutAddress, nil, "", shown,
			checkoutAttemptKey("checkout-cart-aggregate-"+uuid.NewString()),
		)
		checkoutNumber <- number
		checkoutDone <- placeErr
	}()
	waitForApplicationLock(t, "checkout-cart-aggregate", checkoutDone)

	reorderDone := make(chan error, 1)
	reorderResult := make(chan cart.Reorder, 1)
	go func() {
		result, reorderErr := cart.NewStore(reorderPool).Reorder(ctx, basket, sourceOrder)
		reorderResult <- result
		reorderDone <- reorderErr
	}()
	waitForApplicationLock(t, "reorder-cart-aggregate", reorderDone)

	if err := variantTx.Commit(ctx); err != nil {
		t.Fatalf("release checkout variant: %v", err)
	}
	if err := <-checkoutDone; err != nil {
		t.Fatalf("checkout after variant release: %v", err)
	}
	placedOrder := <-checkoutNumber
	if err := <-reorderDone; err != nil {
		t.Fatalf("reorder after checkout: %v", err)
	}
	if result := <-reorderResult; result.Added != 1 || len(result.Skipped) != 0 {
		t.Fatalf("reorder result = %+v, want one added line", result)
	}

	var checkoutLines, reorderedInOrder int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE ol.variant_id = $2),
		       count(*) FILTER (WHERE ol.variant_id = $3)
		FROM order_lines ol
		JOIN orders o ON o.id = ol.order_id
		WHERE o.order_number = $1`, placedOrder, checkoutVariant, reorderedVariant).
		Scan(&checkoutLines, &reorderedInOrder); err != nil {
		t.Fatalf("read placed order lines: %v", err)
	}
	if checkoutLines != 1 || reorderedInOrder != 0 {
		t.Errorf("placed order has checkout/reordered lines %d/%d, want 1/0",
			checkoutLines, reorderedInOrder)
	}

	var checkoutInCart, reorderedInCart int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE variant_id = $2),
		       count(*) FILTER (WHERE variant_id = $3)
		FROM cart_items WHERE cart_id = $1`, basket, checkoutVariant, reorderedVariant).
		Scan(&checkoutInCart, &reorderedInCart); err != nil {
		t.Fatalf("read cart after checkout and reorder: %v", err)
	}
	if checkoutInCart != 0 || reorderedInCart != 1 {
		t.Errorf("cart has checkout/reordered lines %d/%d, want 0/1",
			checkoutInCart, reorderedInCart)
	}
}
