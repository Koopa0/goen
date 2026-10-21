package auth

import (
	"context"

	"github.com/koopa0/goen/internal/cart/cartitem"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

type CartRecoveryView struct {
	Welcome  bool
	Next     string
	Notice   string
	Retry    string
	Contents CartRecoveryContents
}

type CartRecoveryContents struct {
	BeforeSignIn []cartitem.Item
	Account      []cartitem.Item
}

func CartRecoveryMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyCartMergeRecoveryTitle)}
}
