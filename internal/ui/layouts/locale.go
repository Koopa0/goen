package layouts

import (
	"context"

	"github.com/koopa0/goen/internal/web"
)

func returnPath(ctx context.Context) string {
	if p := web.RequestPath(ctx); p != "" {
		return p
	}
	return "/"
}
