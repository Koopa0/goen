package layouts

import (
	"context"

	"github.com/koopa0/goen/internal/web"
)

// returnPath is where the language switch should send the visitor back to.
//
// It comes from the request path the middleware recorded, so switching
// re-renders the page being read. The server validates it again on the way
// back — this is convenience, not a permission.
func returnPath(ctx context.Context) string {
	if p := web.RequestPath(ctx); p != "" {
		return p
	}
	return "/"
}
