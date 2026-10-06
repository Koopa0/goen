// Package contactsubject owns the durable categories of a contact message.
package contactsubject

import (
	"slices"

	"github.com/koopa0/goen/internal/i18n"
)

type Subject string

const (
	// i18n-exempt: durable contact_messages.subject value, independent of locale.
	SubjectOrder Subject = "訂單問題"
	// i18n-exempt: durable contact_messages.subject value, independent of locale.
	SubjectReturns Subject = "退換貨"
	// i18n-exempt: durable contact_messages.subject value, independent of locale.
	SubjectWarranty Subject = "保固維修"
	// i18n-exempt: durable contact_messages.subject value, independent of locale.
	SubjectProduct Subject = "商品諮詢"
	// i18n-exempt: durable contact_messages.subject value, independent of locale.
	SubjectPartnership Subject = "合作提案"
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
