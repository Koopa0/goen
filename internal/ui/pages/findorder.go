package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// FindOrderView carries one Error for both inputs: naming the wrong half confirms an order number.
type FindOrderView struct {
	Number string
	Email  string
	Error  string
}

func FindOrderMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyFindOrderTitle)}
}
