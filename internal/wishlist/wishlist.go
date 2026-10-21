// Package wishlist is the products a customer saved to buy later; it holds the
// page, save, remove and WishlistHas, the read the product page asks; it does not
// hold the account overview (account), the cart (cart) or the back-in-stock
// request (backinstock).
//
// The page stays at /account/wishlist, one of the customer's own pages, and is
// served only to the signed-in customer, whose list it is.
package wishlist
