package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Returns serves GET /admin/returns.
func (h *Handler) Returns(w http.ResponseWriter, r *http.Request) {
	queue, err := h.store.Returns(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read return queue", "error", err)
		h.serverError(w, r)
		return
	}
	for _, issue := range queue.payoutIssues {
		h.log.ErrorContext(r.Context(), "return payout no longer fits its sources",
			"return_id", issue.returnID, "error", issue.err)
	}
	view := pages.AdminReturnsView{
		Rows:   queue.Rows,
		Notice: noticeFor(r),
	}
	web.Render(w, r, h.log, http.StatusOK, pages.AdminReturns(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageReturns)}, view))
}

// Decide serves POST /admin/returns/{id}/decide. Approving pays money back, so
// a refusal from the database or from Stripe is reported and never swallowed.
func (h *Handler) Decide(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.Decide(r.Context(), r.PathValue("id"),
		r.PostFormValue("decision"), r.PostFormValue("resolution"),
		r.PostFormValue("rejection_ground"), staffID(r))
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/returns?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefundIncomplete):
		// A payout may preserve a database refusal as its cause, but once approval
		// committed the operator needs the recovery notice, not the generic
		// "decision refused" notice. Test this before ErrRefused.
		h.log.ErrorContext(r.Context(), "decide return",
			"return", r.PathValue("id"), "error", err)
		http.Redirect(w, r, "/admin/returns?refundfailed=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "return decision refused",
			"return", r.PathValue("id"), "error", err)
		http.Redirect(w, r, "/admin/returns?refused=1", http.StatusSeeOther)
	default:
		// Infrastructure errors which occurred before this became a durable payout
		// recovery land here.
		h.log.ErrorContext(r.Context(), "decide return",
			"return", r.PathValue("id"), "error", err)
		http.Redirect(w, r, "/admin/returns?refundfailed=1", http.StatusSeeOther)
	}
}

// Inspect serves POST /admin/returns/{id}/inspect, one form per parcel.
func (h *Handler) Inspect(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}

	lines, parseErr := inspectionLines(r)
	if parseErr != nil {
		h.log.WarnContext(r.Context(), "return inspection rejected",
			"return", r.PathValue("id"), "error", parseErr)
		http.Redirect(w, r, "/admin/returns?badcount=1", http.StatusSeeOther)
		return
	}

	err := h.store.InspectReturn(r.Context(), r.PathValue("id"), lines, staffID(r))
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/returns?inspected=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid):
		http.Redirect(w, r, "/admin/returns?badcount=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "return inspection refused",
			"return", r.PathValue("id"), "error", err)
		http.Redirect(w, r, "/admin/returns?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "inspect return",
			"return", r.PathValue("id"), "error", err)
		h.serverError(w, r)
	}
}

// inspectionLines reads the per-line counts off the form, keyed by line id for
// parcelLines' reason: two drifted lists would restock the wrong variant.
func inspectionLines(r *http.Request) ([]ReturnLineInspection, error) {
	var out []ReturnLineInspection
	for name, values := range r.PostForm {
		rest, ok := strings.CutPrefix(name, "received_")
		if !ok || len(values) == 0 {
			continue
		}
		lineID, err := uuid.Parse(rest)
		if err != nil {
			return nil, fmt.Errorf("field %q does not name an order line: %w", name, err)
		}
		received, err := strconv.ParseInt(strings.TrimSpace(values[0]), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("received count on %s: %w", rest, err)
		}
		// Absent means zero: an empty restock box says "none of it", and reading
		// it as "all of it" would put damaged goods back on the shelf.
		var restocked int64
		if raw := strings.TrimSpace(r.PostFormValue("restocked_" + rest)); raw != "" {
			if restocked, err = strconv.ParseInt(raw, 10, 32); err != nil {
				return nil, fmt.Errorf("restocked count on %s: %w", rest, err)
			}
		}
		out = append(out, ReturnLineInspection{
			OrderLineID: lineID,
			Received:    int32(received),
			Restocked:   int32(restocked),
			Note:        strings.TrimSpace(r.PostFormValue("note_" + rest)),
		})
	}
	if len(out) == 0 {
		return nil, errors.New("the form carried no line counts")
	}
	return out, nil
}

// Complete serves POST /admin/returns/{id}/complete.
func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.CompleteReturn(r.Context(), r.PathValue("id"),
		r.PostFormValue("resolution"), staffID(r))
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/returns?closed=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "return completion refused",
			"return", r.PathValue("id"), "error", err)
		http.Redirect(w, r, "/admin/returns?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "complete return",
			"return", r.PathValue("id"), "error", err)
		h.serverError(w, r)
	}
}
