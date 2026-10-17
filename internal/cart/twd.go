package cart

import "github.com/koopa0/goen/internal/money"

// twd formats cents for the cart and the checkout. ui/pages and the other features with views of their own keep the same one-line alias.
func twd(cents int64) string { return money.TWD(cents) }
