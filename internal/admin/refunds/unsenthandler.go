package refunds

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/order/returns"
	"github.com/koopa0/goen/internal/payment/refund"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/respond"
)

// RefundUnsent serves POST /admin/orders/{number}/refund-unsent. The first
// POST only renders what the refund pays; the confirmed one moves money.
func (h *Handler) RefundUnsent(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !order.ValidNumber(number) {
		http.NotFound(w, r)
		return
	}
	back := "/admin/orders/" + number
	req, err := unsentRequestOf(r)
	if err != nil {
		h.log.WarnContext(r.Context(), "unsent refund form", "order", number, "error", err)
		http.Redirect(w, r, back+"?unsentbounds=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
		return
	}
	if h.confirmRefundUnsent(w, r, number, req) {
		return
	}
	err = h.store.RefundUnsent(r.Context(), number, req)
	outcome, ok := unsentNotice(err)
	if !ok {
		h.log.ErrorContext(r.Context(), "unsent refund", "order", number, "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	if err != nil {
		h.log.Log(r.Context(), outcome.Level, "unsent refund", "order", number, "error", err)
	}
	http.Redirect(w, r, back+outcome.Query, http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
}

// unsentRequestOf reads the `qty_<order_line_id>` fields the dispatch form
// also uses, the reason code and the note.
func unsentRequestOf(r *http.Request) (*UnsentRequest, error) {
	req := &UnsentRequest{
		Units:  map[uuid.UUID]int32{},
		Reason: r.PostFormValue("reason"),
		Note:   r.PostFormValue("note"),
	}
	for name, values := range r.PostForm {
		rest, ok := strings.CutPrefix(name, "qty_")
		if !ok || len(values) == 0 {
			continue
		}
		lineID, err := uuid.Parse(rest)
		if err != nil {
			return nil, fmt.Errorf("field %q does not name an order line: %w", name, err)
		}
		raw := strings.TrimSpace(values[0])
		if raw == "" {
			continue
		}
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("quantity %q on %s is not a count", raw, rest)
		}
		req.Units[lineID] = int32(n)
	}
	return req, nil
}

func (h *Handler) confirmRefundUnsent(w http.ResponseWriter, r *http.Request, number string, req *UnsentRequest) bool {
	preview, err := h.store.UnsentRefundPreview(r.Context(), number, req)
	if errors.Is(err, ErrNotFound) {
		admin.NotFound(w, r, h.log)
		return true
	}
	if err != nil {
		outcome, ok := unsentNotice(err)
		if !ok {
			h.log.ErrorContext(r.Context(), "read unsent refund", "order", number, "error", err)
			admin.ServerError(w, r, h.log)
			return true
		}
		h.log.WarnContext(r.Context(), "unsent refund not offered", "order", number, "error", err)
		http.Redirect(w, r, "/admin/orders/"+number+outcome.Query, http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
		return true
	}
	view := unsentConfirmation(number, &preview)
	view.Note = r.PostFormValue("note")
	status := http.StatusOK
	switch {
	case r.PostFormValue("confirm") != "refund-unsent" || r.PostFormValue("total") != view.Total():
	case !view.Resume && (strings.TrimSpace(view.Note) == "" || !returns.ValidResolution(view.Note)):
		// The note becomes the refund's resolution and the audit's; a resume
		// reuses the one already recorded.
		view.NoteInvalid = true
		status = http.StatusUnprocessableEntity
	default:
		return false
	}
	respond.Component(w, r, h.log, status, ConfirmUnsentRefund(frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminUnsentTitle)}, view))
	return true
}

func unsentConfirmation(number string, p *UnsentRefund) UnsentRefundConfirmation {
	return UnsentRefundConfirmation{
		OrderNumber: number, Lines: unsentLines(p.Units), Reason: p.Reason,
		RefundCents: p.RefundCents, CardCents: p.CardCents, CreditCents: p.CreditCents,
		Resume: p.Open,
	}
}

func unsentLines(units []refund.UnsentUnit) []UnsentLine {
	lines := make([]UnsentLine, len(units))
	for i, u := range units {
		lines[i] = UnsentLine{OrderLineID: u.LineID.String(), Name: u.Name, Label: u.Label, Quantity: u.Quantity}
	}
	return lines
}
