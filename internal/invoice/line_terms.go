package invoice

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/i18n"
)

type TaxType string

const (
	Taxable   TaxType = "taxable"
	Exempt    TaxType = "exempt"
	ZeroRated TaxType = "zero_rated"
)

func (tax TaxType) Known() bool {
	return tax == Taxable || tax == Exempt || tax == ZeroRated
}

func ProductTaxTypes() []TaxType { return []TaxType{Taxable, Exempt} }

func (tax TaxType) Label(ctx context.Context) string {
	switch tax {
	case Taxable:
		return i18n.T(ctx, i18n.KeyInvoiceTaxable)
	case Exempt:
		return i18n.T(ctx, i18n.KeyInvoiceExempt)
	case ZeroRated:
		return i18n.T(ctx, i18n.KeyInvoiceZeroRated)
	default:
		return ""
	}
}

type ItemUnit string

const DefaultUnit ItemUnit = "個" // i18n-exempt: invoice units are Chinese shop values in either interface locale.

func (unit ItemUnit) Valid() bool {
	return utf8.ValidString(string(unit)) && strings.TrimSpace(string(unit)) != "" && utf8.RuneCountInString(string(unit)) <= 6 && !strings.ContainsFunc(string(unit), unicode.IsControl)
}

func CommonItemUnits() []ItemUnit {
	// i18n-exempt: these are invoice values, not translated interface labels.
	return []ItemUnit{DefaultUnit, "件", "盒", "組", "本", "瓶", "包", "雙", "台", "支", "張", "份", "公斤"}
}

type LineTerms struct {
	TaxType TaxType  `json:"tax_type"`
	Unit    ItemUnit `json:"invoice_unit"`
}

func (facts LineTerms) Validate(ctx context.Context) map[string]string {
	errs := make(map[string]string)
	if !facts.TaxType.Known() || facts.TaxType == ZeroRated {
		errs["tax_type"] = i18n.T(ctx, i18n.KeyInvoiceProductTaxInvalid)
	}
	if !facts.Unit.Valid() {
		errs["invoice_unit"] = i18n.T(ctx, i18n.KeyInvoiceUnitInvalid)
	}
	return errs
}

func (tax TaxType) ecpayCode() string {
	switch tax {
	case Taxable:
		return "1"
	case Exempt:
		return "3"
	default:
		return ""
	}
}
