package returns

import (
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

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
