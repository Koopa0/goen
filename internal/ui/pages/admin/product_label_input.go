package admin

import (
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/product/labelling"
)

// LabelInput is the labelling editor's form as the staff member typed it: every
// field is still a string. The product handler validates it before anything is
// stored.
type LabelInput struct {
	Origin               string
	OriginEn             string
	DomesticPartyName    string
	DomesticPartyPhone   string
	DomesticPartyAddress string
	NetQuantity          string
	NetUnit              labelling.NetUnit
	MinAgeMonths         string
}

// LabelField is one free-text field of the editor: its form name, caption and
// the longest value the store accepts.
type LabelField struct {
	Name    string
	Caption i18n.Key
	Value   string
	Limit   int
}

// TextFields lists the free-text fields once, for the form to draw and for the
// validator to bound, so the hint and the refusal cannot disagree.
func (f *LabelInput) TextFields() []LabelField {
	if f == nil {
		f = &LabelInput{}
	}
	return []LabelField{
		{Name: "origin", Caption: i18n.KeyProductLabelOrigin, Value: f.Origin, Limit: 100},
		{Name: "origin_en", Caption: i18n.KeyProductLabelOriginEn, Value: f.OriginEn, Limit: 100},
		{Name: "domestic_party_name", Caption: i18n.KeyProductLabelDomesticPartyName, Value: f.DomesticPartyName, Limit: 200},
		{Name: "domestic_party_phone", Caption: i18n.KeyProductLabelDomesticPartyPhone, Value: f.DomesticPartyPhone, Limit: 40},
		{Name: "domestic_party_address", Caption: i18n.KeyProductLabelDomesticPartyAddress, Value: f.DomesticPartyAddress, Limit: 500},
	}
}

func (f LabelField) LimitText() string { return strconv.Itoa(f.Limit) }
