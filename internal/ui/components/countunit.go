package components

import (
	"context"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// CountUnit is the words of a counted message after its number, so a stat can set the figure and the unit apart.
func CountUnit(ctx context.Context, k i18n.Key, n int64) string {
	return strings.TrimSpace(strings.TrimLeft(i18n.Count(ctx, k, n, n), "0123456789"))
}
