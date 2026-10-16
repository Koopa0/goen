package buyer

import "github.com/koopa0/goen/internal/money"

// twd formats cents for the order page and the return form. ui/pages, payment, site and email each keep the same one-line alias for their own views.
func twd(cents int64) string { return money.TWD(cents) }
