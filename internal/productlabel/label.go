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

func (u NetUnit) Label(ctx context.Context) string {
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
		return i18n.T(ctx, i18n.KeyLabelPiece)
	default:
		return ""
	}
}

type Facts struct {
	Origin             string
	ResponsibleName    string
	ResponsiblePhone   string
	ResponsibleAddress string
	NetQuantity        string
	NetUnit            NetUnit
	MinAgeMonths       *int16
}

type Fact struct {
	Label i18n.Key
	Value string
}

func (f *Facts) Rows(ctx context.Context) []Fact {
	if f == nil {
		return nil
	}
	var rows []Fact
	for _, r := range []Fact{
		{Label: i18n.KeyLabelOrigin, Value: f.Origin},
		{Label: i18n.KeyLabelResponsibleName, Value: f.ResponsibleName},
		{Label: i18n.KeyLabelResponsiblePhone, Value: f.ResponsiblePhone},
		{Label: i18n.KeyLabelResponsibleAddress, Value: f.ResponsibleAddress},
	} {
		if r.Value != "" {
			rows = append(rows, r)
		}
	}
	if f.NetQuantity != "" && f.NetUnit.Known() {
		rows = append(rows, Fact{Label: i18n.KeyLabelNetContent, Value: f.NetQuantity + " " + f.NetUnit.Label(ctx)})
	}
	if f.MinAgeMonths != nil {
		rows = append(rows, Fact{Label: i18n.KeyLabelMinAge, Value: fmt.Sprintf(i18n.T(ctx, i18n.KeyLabelAgeMonths), *f.MinAgeMonths)})
	}
	return rows
}
