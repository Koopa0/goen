package contact

import (
	"slices"

	"github.com/koopa0/goen/internal/i18n"
)

// Subject is the value persisted in contact_messages.subject and posted by the
// contact form. It is a code, not display text: the words each reader sees come
// from the i18n key the code names (LabelKey).
type Subject string

const (
	// SubjectOrder is a question about an order.
	SubjectOrder Subject = "order"
	// SubjectReturns is a return or refund.
	SubjectReturns Subject = "returns"
	// SubjectWarranty is a warranty or repair.
	SubjectWarranty Subject = "warranty"
	// SubjectProduct is a question about a product.
	SubjectProduct Subject = "product"
	// SubjectPartnership is a business proposal.
	SubjectPartnership Subject = "partnership"
)

type Choice struct {
	Value Subject
	Key   i18n.Key
}

var choices = [...]Choice{
	{SubjectOrder, i18n.KeySubjectOrder},
	{SubjectReturns, i18n.KeySubjectReturns},
	{SubjectWarranty, i18n.KeySubjectWarranty},
	{SubjectProduct, i18n.KeySubjectProduct},
	{SubjectPartnership, i18n.KeySubjectPartnership},
}

func Choices() []Choice {
	return slices.Clone(choices[:])
}

func (s Subject) LabelKey() (i18n.Key, bool) {
	for _, choice := range choices {
		if s == choice.Value {
			return choice.Key, true
		}
	}
	return "", false
}

func (s Subject) Known() bool {
	_, ok := s.LabelKey()
	return ok
}
