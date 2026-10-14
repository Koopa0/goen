package admin

import (
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func (v VariantsView) Refusals() []layouts.Refusal {
	out := make([]layouts.Refusal, 0, len(v.Variants))
	for i := range v.Variants {
		row := &v.Variants[i]
		out = append(out, layouts.SummarizeRefusal(i18n.KeyAdminRefusalStock, map[string]string{"delta": row.DeltaError}, layouts.RefusalField{Key: "delta", ID: "adj-" + row.SKU}))
	}
	return out
}

func (v ReturnsView) InspectionRefusals() []layouts.Refusal {
	out := make([]layouts.Refusal, 0, len(v.Rows))
	for i := range v.Rows {
		row := &v.Rows[i]
		var fields []layouts.RefusalField
		if len(row.Lines) > 0 {
			fields = append(fields, layouts.RefusalField{Key: "inspect", ID: "recv-" + row.ID + "-" + row.Lines[0].OrderLineID})
		}
		errors := map[string]string{"inspect": v.FieldRefusal(row.ID, "inspect")}
		out = append(out, layouts.SummarizeRefusal(i18n.KeyAdminRefusalInspection, errors, fields...))
	}
	return out
}

func (v *ProductView) VariantRefusal() layouts.Refusal {
	fields := make([]layouts.RefusalField, 0, 7+len(v.Options))
	fields = append(fields, layouts.RefusalField{Key: "sku", ID: "v-sku"}, layouts.RefusalField{Key: "price", ID: "v-price"}, layouts.RefusalField{Key: "compare", ID: "v-compare"})
	if len(v.Options) > 0 {
		fields = append(fields, layouts.RefusalField{Key: "options", ID: "v-opt-" + v.Options[0].ID})
	}
	fields = append(fields, layouts.RefusalField{Key: "safety", ID: "v-safety"}, layouts.RefusalField{Key: "parcel_longest", ID: "v-longest"}, layouts.RefusalField{Key: "parcel_sum", ID: "v-sum"}, layouts.RefusalField{Key: "parcel_weight", ID: "v-weight"})
	return layouts.SummarizeRefusal(i18n.KeyAdminProdVariantAdd, v.Errors, fields...)
}
