// Package item names what is in a cart as the cart reports it to a feature
// that lists it elsewhere: the cart-recovery page shows the items of the cart a
// browser held before sign-in beside those of the account's own. Features reach
// the cart only through interfaces they declare, so the value those interfaces
// promise lives here, where nothing has to import the cart. AddOutcome is the
// other thing the cart reports: what adding an item came to, carried back to the
// product page or the wishlist the form was on.
package item

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
