package account

import "github.com/koopa0/goen/internal/money"

// twd formats cents for the account page. ui/pages, payment, loyalty, site and letter each keep the same one-line alias for their own views.
func twd(cents int64) string { return money.TWD(cents) }
