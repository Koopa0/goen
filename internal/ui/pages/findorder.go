package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// FindOrderView carries a refused lookup's values back into the form. The one
// Error is not per-field: the two inputs are one credential, and saying which
// half was wrong would confirm that an order number is real.
type FindOrderView struct {
	Number string
	Email  string
	Error  string
}

// FindOrderMeta is the document shell.
func FindOrderMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyFindOrderTitle)}
}
