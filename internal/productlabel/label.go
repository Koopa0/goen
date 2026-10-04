// Package productlabel validates optional shop-supplied facts; it does not
// decide which labelling duties apply to a product.
package productlabel

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/internal/i18n"
)

type NetUnit string

const (
	Gram       NetUnit = "g"
	Kilogram   NetUnit = "kg"
	Millilitre NetUnit = "ml"
	Litre      NetUnit = "l"
	Piece      NetUnit = "piece"
)

func (u NetUnit) Known() bool {
	switch u {
	case Gram, Kilogram, Millilitre, Litre, Piece:
		return true
	default:
		return false
	}
}

func Units() []NetUnit { return []NetUnit{Gram, Kilogram, Millilitre, Litre, Piece} }

func (u NetUnit) Symbol(ctx context.Context) string {
	switch u {
	case Gram:
		return "g"
	case Kilogram:
		return "kg"
	case Millilitre:
		return "mL"
	case Litre:
		return "L"
	case Piece:
		return i18n.T(ctx, i18n.KeyProductLabelPiece)
	default:
		return ""
	}
}

type Facts struct {
	Origin                  string
	ResponsiblePartyName    string
	ResponsiblePartyPhone   string
	ResponsiblePartyAddress string
	NetQuantity             string
	NetUnit                 NetUnit
	MinAgeMonths            *int16
}

type Fact struct {
	Term  i18n.Key
	Value string
}

func (f *Facts) Rows(ctx context.Context) []Fact {
	if f == nil {
		return nil
	}
	var rows []Fact
	for _, r := range []Fact{
		{Term: i18n.KeyProductLabelOrigin, Value: f.Origin},
		{Term: i18n.KeyProductLabelResponsiblePartyName, Value: f.ResponsiblePartyName},
		{Term: i18n.KeyProductLabelResponsiblePartyPhone, Value: f.ResponsiblePartyPhone},
		{Term: i18n.KeyProductLabelResponsiblePartyAddress, Value: f.ResponsiblePartyAddress},
	} {
		if r.Value != "" {
			rows = append(rows, r)
		}
	}
	if f.NetQuantity != "" && f.NetUnit.Known() {
		rows = append(rows, Fact{Term: i18n.KeyProductLabelNetContent, Value: f.NetQuantity + " " + f.NetUnit.Symbol(ctx)})
	}
	if f.MinAgeMonths != nil {
		rows = append(rows, Fact{Term: i18n.KeyProductLabelMinAge, Value: fmt.Sprintf(i18n.T(ctx, i18n.KeyProductLabelAgeMonths), *f.MinAgeMonths)})
	}
	return rows
}
