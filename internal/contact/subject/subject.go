// Package subject owns the durable categories of a contact message.
package subject

import (
	"slices"

	"github.com/koopa0/goen/internal/i18n"
)

type Subject string

const (
	// i18n-exempt: durable contact_messages.subject value, independent of locale.
	Order Subject = "訂單問題"
	// i18n-exempt: durable contact_messages.subject value, independent of locale.
	Returns Subject = "退換貨"
	// i18n-exempt: durable contact_messages.subject value, independent of locale.
	Warranty Subject = "保固維修"
	// i18n-exempt: durable contact_messages.subject value, independent of locale.
	Product Subject = "商品諮詢"
	// i18n-exempt: durable contact_messages.subject value, independent of locale.
	Partnership Subject = "合作提案"
)

type Choice struct {
	Value Subject
	Key   i18n.Key
}

var choices = [...]Choice{
	{Order, i18n.KeySubjectOrder},
	{Returns, i18n.KeySubjectReturns},
	{Warranty, i18n.KeySubjectWarranty},
	{Product, i18n.KeySubjectProduct},
	{Partnership, i18n.KeySubjectPartnership},
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
