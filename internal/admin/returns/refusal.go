package returns

import (
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
)

func (v View) InspectionRefusals() []widget.Refusal {
	out := make([]widget.Refusal, 0, len(v.Rows))
	for i := range v.Rows {
		row := &v.Rows[i]
		var fields []widget.RefusalField
		if len(row.Lines) > 0 {
			fields = append(fields, widget.RefusalField{Key: "inspect", ID: "recv-" + row.ID + "-" + row.Lines[0].OrderLineID})
		}
		errors := map[string]string{"inspect": v.FieldRefusal(row.ID, "inspect")}
		out = append(out, widget.SummarizeRefusal(i18n.KeyAdminRefusalInspection, errors, fields...))
	}
	return out
}
