// Package cartitem is what the cart reports about its items to a feature that
// shows them elsewhere; it holds Item, a variant in a cart and how many of it,
// and AddOutcome, what adding one came to; it does not hold the cart, its
// prices or its store (cart).
//
// The cart-recovery page shows the items of the cart a browser held before
// sign-in beside those of the account's own, and the product page and the
// wishlist show what adding an item came to. Features reach the cart only
// through interfaces they declare, so the values those interfaces promise live
// here, where nothing has to import the cart.
package cartitem

import "github.com/google/uuid"

// Item is one product variant in a cart and how many of it, with what a
// customer recognises it by: its name, its options and its photo. It carries no
// price or stock: a listing outside the cart page makes no offer.
type Item struct {
	VariantID uuid.UUID
	Name      string
	// Label joins the variant's option values; it is "" for a product with
	// none.
	Label    string
	Quantity int32
	// ImageURL is "" when the product has no photo.
	ImageURL    string
	ImageSrcset string
	ImageAlt    string
}
