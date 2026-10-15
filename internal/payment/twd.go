package payment

import "github.com/koopa0/goen/internal/money"

// twd formats cents for the pay page. ui/pages and email each keep the same one-line alias for their own views.
func twd(cents int64) string { return money.TWD(cents) }
