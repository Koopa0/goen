package auth

import (
	"context"

	"github.com/koopa0/goen/internal/cart/cartitem"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
)

type CartRecoveryView struct {
	Welcome  bool
	Next     string
	Notice   widget.Result
	Retry    string
	Contents CartRecoveryContents
}

type CartRecoveryContents struct {
	BeforeSignIn []cartitem.Item
	Account      []cartitem.Item
}

func CartRecoveryMeta(ctx context.Context) frame.Page {
	return frame.Page{Title: i18n.T(ctx, i18n.KeyCartMergeRecoveryTitle)}
}
