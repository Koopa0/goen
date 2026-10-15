package layouts

import (
	"context"
	"fmt"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// Departments reads the header's category row, the same rows the home tiles
// show, so a sentence naming them cannot drift from the catalogue.
func Departments(ctx context.Context) string {
	items := TopNavFrom(ctx)
	names := make([]string, 0, len(items))
	for i := range items {
		names = append(names, items[i].Name)
	}
	return JoinList(ctx, names)
}

// DescribeDepartments formats the list into the sentence k holds. A description left out is better than one naming nothing.
func DescribeDepartments(ctx context.Context, k i18n.Key) string {
	list := Departments(ctx)
	if list == "" {
		return ""
	}
	return fmt.Sprintf(i18n.T(ctx, k), list)
}

// JoinList joins names the way the reader's language does. English puts "and" before the last; Chinese uses 、 throughout.
func JoinList(ctx context.Context, names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	last := len(names) - 1
	return strings.Join(names[:last], i18n.T(ctx, i18n.KeyListSeparator)) +
		i18n.T(ctx, i18n.KeyListLastSeparator) + names[last]
}
