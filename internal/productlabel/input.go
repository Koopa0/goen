package productlabel

import (
	"context"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/i18n"
)

type Input struct {
	Origin               string
	OriginEn             string
	DomesticPartyName    string
	DomesticPartyPhone   string
	DomesticPartyAddress string
	NetQuantity          string
	NetUnit              NetUnit
	MinAgeMonths         string
}

type TextField struct {
	Name    string
	Caption i18n.Key
	Value   string
	Limit   int
}

func (f *Input) TextFields() []TextField {
	if f == nil {
		f = &Input{}
	}
	return []TextField{
		{Name: "origin", Caption: i18n.KeyProductLabelOrigin, Value: f.Origin, Limit: 100},
		{Name: "origin_en", Caption: i18n.KeyProductLabelOriginEn, Value: f.OriginEn, Limit: 100},
		{Name: "domestic_party_name", Caption: i18n.KeyProductLabelDomesticPartyName, Value: f.DomesticPartyName, Limit: 200},
		{Name: "domestic_party_phone", Caption: i18n.KeyProductLabelDomesticPartyPhone, Value: f.DomesticPartyPhone, Limit: 40},
		{Name: "domestic_party_address", Caption: i18n.KeyProductLabelDomesticPartyAddress, Value: f.DomesticPartyAddress, Limit: 500},
	}
}

func (f TextField) LimitText() string { return strconv.Itoa(f.Limit) }

func (f *Input) Validate(ctx context.Context) map[string]string {
	errs := make(map[string]string)
	for _, field := range f.TextFields() {
		if !utf8.ValidString(field.Value) || utf8.RuneCountInString(strings.TrimSpace(field.Value)) > field.Limit || strings.ContainsFunc(field.Value, unicode.IsControl) {
			errs[field.Name] = i18n.T(ctx, i18n.KeyProductLabelTextInvalid)
		}
	}
	quantity, unit := strings.TrimSpace(f.NetQuantity), strings.TrimSpace(string(f.NetUnit))
	if quantity != "" || unit != "" {
		_, ok := QuantityHundredths(quantity)
		if !ok || !NetUnit(unit).Known() {
			errs["net_quantity"] = i18n.T(ctx, i18n.KeyProductLabelNetInvalid)
			errs["net_unit"] = i18n.T(ctx, i18n.KeyProductLabelNetInvalid)
		}
	}
	if _, ok := AgeMonths(f.MinAgeMonths); !ok {
		errs["min_age_months"] = i18n.T(ctx, i18n.KeyProductLabelAgeInvalid)
	}
	return errs
}

func QuantityHundredths(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	whole, fraction, decimal := strings.Cut(raw, ".")
	if len(whole) < 1 || len(whole) > 8 || (decimal && (len(fraction) < 1 || len(fraction) > 2)) {
		return 0, false
	}
	for _, part := range []string{whole, fraction} {
		for _, r := range part {
			if r < '0' || r > '9' {
				return 0, false
			}
		}
	}
	base, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, false
	}
	fraction += strings.Repeat("0", 2-len(fraction))
	part, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return 0, false
	}
	n := base*100 + part
	return n, n > 0
}

func AgeMonths(raw string) (int16, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(raw, 10, 16)
	return int16(n), err == nil && n >= 0 && n <= 216
}
