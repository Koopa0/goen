package i18n

import (
	"context"
	"strings"
)

var (
	KeyListSeparator = key("common.listseparator", Message{
		ZhHant: "、",
		En:     ", ",
	})

	// What goes before the last of the names JoinList joins.
	keyListLastSeparator = key("common.listseparator.last", Message{
		ZhHant: "、",
		En:     " and ",
	})
)

// JoinList joins names the way the reader's language does. English puts "and" before the last; Chinese uses 、 throughout.
func JoinList(ctx context.Context, names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	last := len(names) - 1
	return strings.Join(names[:last], T(ctx, KeyListSeparator)) +
		T(ctx, keyListLastSeparator) + names[last]
}
