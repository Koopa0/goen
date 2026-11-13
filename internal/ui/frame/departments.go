package frame

import (
	"context"
	"fmt"
	"unicode/utf8"

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
	return i18n.JoinList(ctx, names)
}

// DescribeDepartments formats the list into the sentence k holds. A description left out is better than one naming nothing.
func DescribeDepartments(ctx context.Context, k i18n.Key) string {
	list := Departments(ctx)
	if list == "" {
		return ""
	}
	return fmt.Sprintf(i18n.T(ctx, k), list)
}

// Initial is the first character of the name, which a department without a
// photograph shows on its tone.
func (n NavItem) Initial() string {
	r, _ := utf8.DecodeRuneInString(n.Name)
	if r == utf8.RuneError {
		return ""
	}
	return string(r)
}
