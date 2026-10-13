// Package labelling is the vocabulary of the facts a shop declares about a
// product: where it comes from, who answers for it at home, how much it holds,
// the youngest age it suits. It does not decide which labelling duties apply to
// a product, and it neither validates a form nor lays out the rows a page shows.
package labelling

import (
	"context"

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
	Origin               string
	DomesticPartyName    string
	DomesticPartyPhone   string
	DomesticPartyAddress string
	NetQuantity          string
	NetUnit              NetUnit
	MinAgeMonths         *int16
}
