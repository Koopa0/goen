package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// FindOrderView carries a refused lookup back; one Error for both inputs,
// because naming the wrong half confirms an order number.
type FindOrderView struct {
	Number string
	Email  string
	Error  string
}

// FindOrderMeta is the document shell.
func FindOrderMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyFindOrderTitle)}
}
