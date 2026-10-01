package i18n

import (
	"context"
	"fmt"
	"strings"
)

// singulars holds the English wording for exactly one, for the counts whose
// plural form is registered in messages. Chinese has no plural, so it has none.
var singulars = map[Key]string{}

// countKey registers a counted message: enOne is read when the count is 1, and
// the registered En (enMany) otherwise. Both carry one %s or %d for the number.
func countKey(id, zhHant, enOne, enMany string) Key {
	if strings.Count(enOne, "%") != 1 || strings.Count(enMany, "%") != 1 {
		panic("i18n: " + id + " needs exactly one verb in each English form")
	}
	if enOne == enMany {
		panic("i18n: " + id + " has the same English for one and many")
	}
	k := key(id, Message{ZhHant: zhHant, En: enMany})
	singulars[k] = enOne
	return k
}

// Count renders a counted message for n, with shown as the number the reader
// sees (n itself, or n formatted).
func Count(ctx context.Context, k Key, n int64, shown any) string {
	format := T(ctx, k)
	if one, ok := singulars[k]; ok && n == 1 && FromContext(ctx) == En {
		format = one
	}
	return fmt.Sprintf(format, shown)
}
