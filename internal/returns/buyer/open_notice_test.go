package buyer

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

func TestOpenRequestTakesPrecedenceOverStaleFormRefusals(t *testing.T) {
	t.Parallel()
	order := &Order{HasOpen: true, Lines: []Line{{ID: "line"}}}
	draft := &returnDraft{Reason: "Kept reason", Quantities: map[string]string{"line": "2"}}
	for _, refusal := range []web.FieldRefusal{
		{Field: "qty_line", MessageKey: i18n.KeyReturnTooMany},
		{MessageKey: i18n.KeyReturnInvalid},
		{MessageKey: i18n.KeyReturnAlreadyOpen},
		{Field: "reason", MessageKey: i18n.KeyReturnReasonTooLong},
	} {
		key := refusal.MessageKey
		refusals := returnRefusals(order, draft, []web.FieldRefusal{refusal})
		if len(refusals) != 0 {
			t.Errorf("open request retained refusals for %s: %v", key, refusals)
		}
		view := viewOf(t.Context(), order, draft, refusals)
		if view.FormRefusal != "" || view.ReasonRefusal != "" || view.Lines[0].Refusal != "" {
			t.Errorf("open request view retained errors for %s", key)
		}
		if !view.HasDraft || view.Reason != draft.Reason || view.Lines[0].Quantity != "2" {
			t.Error("open request refusal discarded submitted input")
		}
	}
}
