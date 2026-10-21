package payment

import "github.com/koopa0/goen/internal/money"

// twd formats cents for the pay page. email keeps the same one-line alias for its own views.
func twd(cents int64) string { return money.TWD(cents) }
