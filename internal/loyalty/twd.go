package loyalty

import "github.com/koopa0/goen/internal/money"

// twd formats cents for the points page. email keeps the same one-line alias for its own views.
func twd(cents int64) string { return money.TWD(cents) }
