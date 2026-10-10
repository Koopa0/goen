package admin

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// refusalField lists controls in their visual order, independently of map order.
type refusalField struct{ key, id string }

type refusalSummary struct {
	Form  i18n.Key
	Count int
	First string
}

func summarizeRefusal(form i18n.Key, errors map[string]string, fields ...refusalField) refusalSummary {
	out := refusalSummary{Form: form}
	for _, field := range fields {
		if errors[field.key] == "" {
			continue
		}
		out.Count++
		if out.First == "" {
			out.First = field.id
		}
	}
	return out
}

func (v VariantsView) Refusals() []refusalSummary {
	out := make([]refusalSummary, 0, len(v.Variants))
	for i := range v.Variants {
		row := &v.Variants[i]
		out = append(out, summarizeRefusal(i18n.KeyAdminRefusalStock, map[string]string{"delta": row.DeltaError}, refusalField{"delta", "adj-" + row.SKU}))
	}
	return out
}

func (v ReturnsView) InspectionRefusals() []refusalSummary {
	out := make([]refusalSummary, 0, len(v.Rows))
	for i := range v.Rows {
		row := &v.Rows[i]
		fields := make([]refusalField, 0, 2*len(row.Lines))
		errors := map[string]string{}
		for j := range row.Lines {
			line := &row.Lines[j]
			id := "recv-" + row.ID + "-" + line.OrderLineID
			errors[id] = v.FieldRefusal(row.ID, "inspect")
			fields = append(fields, refusalField{id, id})
			if line.Restockable {
				id = "stock-" + row.ID + "-" + line.OrderLineID
				errors[id] = v.FieldRefusal(row.ID, "inspect")
				fields = append(fields, refusalField{id, id})
			}
		}
		out = append(out, summarizeRefusal(i18n.KeyAdminRefusalInspection, errors, fields...))
	}
	return out
}

func (v refusalSummary) Text(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyAdminRefusalSummary, int64(v.Count), v.Count, i18n.T(ctx, v.Form))
}

func (v *ProductView) VariantRefusal() refusalSummary {
	fields := make([]refusalField, 0, 7+len(v.Options))
	fields = append(fields, refusalField{"sku", "v-sku"}, refusalField{"price", "v-price"}, refusalField{"compare", "v-compare"})
	for _, option := range v.Options {
		fields = append(fields, refusalField{"options", "v-opt-" + option.ID})
	}
	fields = append(fields, refusalField{"safety", "v-safety"}, refusalField{"parcel_longest", "v-longest"}, refusalField{"parcel_sum", "v-sum"}, refusalField{"parcel_weight", "v-weight"})
	return summarizeRefusal(i18n.KeyAdminProdVariantAdd, v.Errors, fields...)
}
