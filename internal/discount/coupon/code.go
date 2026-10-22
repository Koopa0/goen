package coupon

import (
	"regexp"
	"strings"

	"github.com/koopa0/goen/internal/web/formtext"
)

// codeShape restates coupons_code_format so a malformed code is a message
// rather than a query.
var codeShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{1,31}$`)

// Normalise is a typed code as it is stored and looked up: width-folded,
// trimmed and upper case, because a code printed on a card is read by a human
// and often typed on a Chinese keyboard left in full-width mode. The fold
// leaves ASCII alone, so every code coupons_code_format admits is its own
// normal form and no two stored codes become one.
func Normalise(s string) string {
	return strings.ToUpper(strings.TrimSpace(formtext.FoldWidth(s)))
}

// ValidCode reports whether code, already normalised, has the shape
// coupons_code_format admits.
func ValidCode(code string) bool { return codeShape.MatchString(code) }
