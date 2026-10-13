package issuer

import (
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/invoice"
)

// knownAllowance is the complete immutable local half of one provider
// allowance-list row. Status is intentionally separate from the filed facts:
// issued/voided is the state being reconciled, while every other field must
// already agree before that state may move.
type knownAllowance struct {
	ID          uuid.UUID
	Number      string
	AmountCents int64
	Status      invoice.DocumentStatus
	IssuedAt    time.Time
	Lines       []invoice.Line
}

func knownAllowanceLines(
	descriptions []string,
	quantities []int32,
	unitPriceCents, amountCents []int64,
	taxTypes, units []string,
) ([]invoice.Line, bool) {
	count := len(descriptions)
	if count == 0 || len(quantities) != count || len(unitPriceCents) != count ||
		len(amountCents) != count || len(taxTypes) != count || len(units) != count {
		return nil, false
	}
	lines := make([]invoice.Line, count)
	for i, description := range descriptions {
		if (invoice.TaxType(taxTypes[i]) != invoice.Taxable && invoice.TaxType(taxTypes[i]) != invoice.Exempt) || !invoice.ItemUnit(units[i]).Valid() {
			return nil, false
		}
		lines[i] = invoice.Line{
			Description: description, Quantity: quantities[i],
			UnitPriceCents: unitPriceCents[i], AmountCents: amountCents[i],
			TaxType: invoice.TaxType(taxTypes[i]), Unit: invoice.ItemUnit(units[i]),
		}
	}
	return lines, true
}

// allowanceKnownFactsMatch requires the provider to repeat the exact document
// we filed. Matching only the globally unique allowance number is insufficient:
// a malformed/wrong-invoice lookup must alarm rather than voiding local tax
// history or being attributed to the in-flight operation.
func allowanceKnownFactsMatch(
	invoiceNumber string, local knownAllowance, remote *AllowanceLookup,
) bool {
	return remote.InvoiceNumber == invoiceNumber &&
		remote.Document.Kind == invoice.DocumentAllowance &&
		remote.Document.Number == local.Number &&
		remote.Document.AmountCents == local.AmountCents &&
		remote.Document.IssuedAt.Equal(local.IssuedAt) &&
		slices.Equal(remote.Document.Lines, local.Lines)
}

// allowanceRequestFactsMatch deliberately ignores Invalid: callers first prove
// that a provider candidate is the exact effect of the frozen send, then branch
// active versus invalid into distinct atomic settlement doors.
func allowanceRequestFactsMatch(expected AllowanceRequest, remote *AllowanceLookup) bool {
	return remote.InvoiceNumber == expected.InvoiceNumber &&
		remote.Document.Kind == invoice.DocumentAllowance &&
		remote.Document.AmountCents == expected.AmountCents &&
		slices.Equal(remote.Document.Lines, expected.Lines)
}
