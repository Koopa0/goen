package checkout

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shipping/postcode"
)

// i18n-exempt: 台 and 臺 are one character in two forms. The district table is published with 臺, and
// neither form is shown as written here.
var tableSpelling = strings.NewReplacer("台", "臺")

// postalMismatchNote names the districts the postcode's first three digits cover when none of them is
// the city and district typed beside it, and is empty when it agrees, when either is blank or when the
// table does not know the digits. The postcode is in one of those districts, not all of them; the list
// reads "A、B" because that is the house join. It is a note and never a refusal: the table is a
// snapshot, and the post office adds and renames districts after it.
func postalMismatchNote(ctx context.Context, a Address) string {
	if len(a.PostalCode) < 3 || a.City == "" || a.District == "" {
		return ""
	}
	prefix := a.PostalCode[:3]
	known := postcode.Districts(prefix)
	if len(known) == 0 {
		return ""
	}
	typed := a.City + a.District
	if slices.ContainsFunc(known, func(d string) bool {
		return tableSpelling.Replace(d) == tableSpelling.Replace(typed)
	}) {
		return ""
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyCheckoutPostalMismatch), prefix, i18n.JoinList(ctx, known), typed)
}
