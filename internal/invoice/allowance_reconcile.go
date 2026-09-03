package invoice

import (
	"slices"
	"time"

	"github.com/google/uuid"
)

// knownAllowance is the complete immutable local half of one provider
// allowance-list row. Status is intentionally separate from the filed facts:
// issued/voided is the state being reconciled, while every other field must
// already agree before that state may move.
type knownAllowance struct {
	ID          uuid.UUID
	Number      string
	AmountCents int64
	Status      string
	IssuedAt    time.Time
	Lines       []Line
}

func knownAllowanceLines(
	descriptions []string,
	quantities []int32,
	unitPriceCents, amountCents []int64,
	taxTypes []string,
) ([]Line, bool) {
	count := len(descriptions)
	if count == 0 || len(quantities) != count || len(unitPriceCents) != count ||
		len(amountCents) != count || len(taxTypes) != count {
		return nil, false
	}
	lines := make([]Line, count)
	for i, description := range descriptions {
		if i >= len(quantities) || i >= len(unitPriceCents) ||
			i >= len(amountCents) || i >= len(taxTypes) {
			return nil, false
		}
		if taxTypes[i] != "taxable" {
			return nil, false
		}
		lines[i] = Line{
			Description: description, Quantity: quantities[i],
			UnitPriceCents: unitPriceCents[i], AmountCents: amountCents[i],
		}
	}
	return lines, true
}

// allowanceKnownFactsMatch requires the provider to repeat the exact document
// we filed. Matching only the globally unique allowance number is insufficient:
// a malformed/wrong-invoice lookup must alarm rather than voiding local tax
// history or being attributed to the in-flight operation.
func allowanceKnownFactsMatch(
	invoiceNumber string, local knownAllowance, remote AllowanceLookup,
) bool {
	return remote.InvoiceNumber == invoiceNumber &&
		remote.Document.Kind == "allowance" &&
		remote.Document.Number == local.Number &&
		remote.Document.AmountCents == local.AmountCents &&
		remote.Document.IssuedAt.Equal(local.IssuedAt) &&
		slices.Equal(remote.Document.Lines, local.Lines)
}

// allowanceRequestFactsMatch deliberately ignores Invalid: callers first prove
// that a provider candidate is the exact effect of the frozen send, then branch
// active versus invalid into distinct atomic settlement doors.
func allowanceRequestFactsMatch(expected AllowanceRequest, remote AllowanceLookup) bool {
	return remote.InvoiceNumber == expected.InvoiceNumber &&
		remote.Document.Kind == "allowance" &&
		remote.Document.AmountCents == expected.AmountCents &&
		slices.Equal(remote.Document.Lines, expected.Lines)
}
