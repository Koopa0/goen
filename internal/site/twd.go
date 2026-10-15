package site

import "github.com/koopa0/goen/internal/money"

// twd formats cents for the shipping policy. ui/pages, payment and email each keep the same one-line alias for their own views.
func twd(cents int64) string { return money.TWD(cents) }
