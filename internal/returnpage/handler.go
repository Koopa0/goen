package returnpage

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	access *orderaccess.Store
	store  *Store
	log    *slog.Logger
}

func NewHandler(s *Store, access *orderaccess.Store, log *slog.Logger) *Handler {
	if s == nil || access == nil || log == nil {
		panic("returnpage: NewHandler requires a store, an access check and a logger")
	}
	return &Handler{store: s, access: access, log: log}
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	o, ok := h.ownOrder(w, r)
	if !ok {
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Returns(
		pages.ReturnsMeta(r.Context(), o.Number), viewOf(r.Context(), o, nil, nil)))
}

func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	o, ok := h.ownOrder(w, r)
	if !ok {
		return
	}

	draft := returnDraftFromForm(r, o)
	req, refusals := draft.request(o)
	if len(refusals) > 0 {
		h.reject(w, r, o, draft, refusals)
		return
	}
	err := h.store.Open(r.Context(), o.Number, returnRequester(r.Context()), req)
	h.respondToOpen(w, r, o, draft, err)
}

type returnDraft struct {
	Reason     string
	Quantities map[string]string
}

func returnDraftFromForm(r *http.Request, o *Order) *returnDraft {
	draft := &returnDraft{Reason: r.PostFormValue("reason"), Quantities: make(map[string]string, len(o.Lines))}
	for _, line := range o.Lines {
		draft.Quantities[line.ID] = r.PostFormValue("qty_" + line.ID)
	}
	return draft
}

func (d *returnDraft) request(o *Order) (*Request, []web.FieldRefusal) {
	req := &Request{Reason: d.Reason, Lines: make(map[string]int32, len(o.Lines))}
	var refusals []web.FieldRefusal
	chosen := false
	quantityRefused := false
	for _, line := range o.Lines {
		raw := d.Quantities[line.ID]
		if raw == "" {
			continue
		}
		quantity, err := strconv.ParseInt(raw, 10, 32)
		key := i18n.Key("")
		switch {
		case err != nil || quantity < 0:
			key = i18n.KeyReturnQuantityInvalid
		case quantity > int64(line.Returnable):
			key = i18n.KeyReturnTooMany
		default:
			req.Lines[line.ID] = int32(quantity)
			chosen = chosen || quantity > 0
		}
		if key != "" {
			quantityRefused = true
			refusals = append(refusals, web.FieldRefusal{Field: "qty_" + line.ID, MessageKey: key})
		}
	}
	if key := returnReasonMessage(d.Reason); key != "" {
		refusals = append(refusals, web.FieldRefusal{Field: "reason", MessageKey: key})
	}
	if !chosen && !quantityRefused {
		refusals = append(refusals, web.FieldRefusal{MessageKey: i18n.KeyReturnInvalid})
	}
	return req, refusals
}

func returnReasonMessage(reason string) i18n.Key {
	switch result := validateReturnReason(reason); result {
	case reasonValid:
		return ""
	case reasonTooLong:
		return i18n.KeyReturnReasonTooLong
	case reasonUnsupportedControls:
		return i18n.KeyReturnReasonUnsupportedControls
	default:
		panic("returnpage: unknown reason validation: " + string(result))
	}
}

func returnRequester(ctx context.Context) uuid.NullUUID {
	u, signedIn := user.FromContext(ctx)
	if !signedIn {
		return uuid.NullUUID{}
	}
	id, err := uuid.Parse(u.ID)
	return uuid.NullUUID{UUID: id, Valid: err == nil}
}

func (h *Handler) respondToOpen(
	w http.ResponseWriter, r *http.Request, o *Order, draft *returnDraft, err error,
) {
	switch {
	case err == nil:
		http.Redirect(w, r, "/orders/"+url.PathEscape(o.Number)+"/return?filed=1", http.StatusSeeOther)
	case errors.Is(err, ErrAlreadyOpen):
		h.reject(w, r, o, draft, []web.FieldRefusal{{MessageKey: i18n.KeyReturnAlreadyOpen}})
	case errors.Is(err, ErrNotReturnable):
		h.reject(w, r, o, draft, []web.FieldRefusal{{MessageKey: i18n.KeyReturnNothingShort}})
	case errors.Is(err, ErrTooMany):
		h.reject(w, r, o, draft, []web.FieldRefusal{{MessageKey: i18n.KeyReturnTooMany}})
	case errors.Is(err, ErrAccountErased):
		h.reject(w, r, o, draft, []web.FieldRefusal{{MessageKey: i18n.KeyReturnAccountErased}})
	case errors.Is(err, ErrInvalid):
		h.reject(w, r, o, draft, []web.FieldRefusal{{MessageKey: i18n.KeyReturnInvalid}})
	default:
		h.log.ErrorContext(r.Context(), "open return request", "order", o.Number, "error", err)
		web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
	}
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request, o *Order, draft *returnDraft, refusals []web.FieldRefusal) {
	fresh, err := h.store.Order(r.Context(), o.Number)
	if err != nil {
		h.log.ErrorContext(r.Context(), "refresh order after return refusal", "order", o.Number, "error", err)
		web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
		return
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.Returns(
		pages.ReturnsMeta(r.Context(), fresh.Number), viewOf(r.Context(), fresh, draft, returnRefusals(fresh, draft, refusals))))
}

// ownOrder: an order number is a per-day counter, so knowing one is not
// authorisation; a stranger gets the 404 an absent order gets.
func (h *Handler) ownOrder(w http.ResponseWriter, r *http.Request) (*Order, bool) {
	number := r.PathValue("number")
	ok, err := h.access.Allows(r, number)
	if err != nil {
		h.log.ErrorContext(r.Context(), "check order access", "order", number, "error", err)
	}
	if !ok {
		h.notFound(w, r)
		return nil, false
	}
	o, err := h.store.Order(r.Context(), number)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.notFound(w, r)
			return nil, false
		}
		h.log.ErrorContext(r.Context(), "read order for return", "order", number, "error", err)
		web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
		return nil, false
	}
	return o, true
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusNotFound, pages.OrderNotFound(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyOrderNotFound)}))
}

func viewOf(ctx context.Context, o *Order, draft *returnDraft, refusals []web.FieldRefusal) pages.ReturnsView {
	v := pages.ReturnsView{
		Number: o.Number, HasOpen: o.HasOpen, HasDraft: draft != nil,
	}
	for i := range o.Lines {
		l := &o.Lines[i]
		line := pages.ReturnsLine{
			ID: l.ID, SKU: l.SKU, Name: l.Name, Label: l.Label,
			UnitCents: l.UnitCents, Returnable: l.Returnable,
		}
		if draft != nil {
			line.Quantity = draft.Quantities[l.ID]
		}
		for _, refusal := range refusals {
			if refusal.Field == line.Field() {
				line.Refusal = i18n.T(ctx, refusal.MessageKey)
			}
		}
		v.Lines = append(v.Lines, line)
	}
	if draft != nil {
		v.Reason = draft.Reason
	}
	for _, refusal := range refusals {
		switch refusal.Field {
		case "":
			v.FormRefusal = i18n.T(ctx, refusal.MessageKey)
		case "reason":
			v.ReasonRefusal = i18n.T(ctx, refusal.MessageKey)
		}
	}
	for i := range o.Existing {
		e := &o.Existing[i]
		v.Existing = append(v.Existing, pages.ReturnsExisting{
			Status: returns.Status(e.Status), Reason: e.Reason,
			Resolution: e.Resolution, CreatedAt: e.CreatedAt, DecidedAt: e.DecidedAt,
		})
	}
	return v
}

func returnRefusals(o *Order, draft *returnDraft, refusals []web.FieldRefusal) []web.FieldRefusal {
	for _, refusal := range refusals {
		switch refusal.MessageKey {
		case i18n.KeyReturnTooMany:
			_, fresh := draft.request(o)
			if len(fresh) > 0 {
				return fresh
			}
		case i18n.KeyReturnNothingShort:
			_, fresh := draft.request(o)
			return append(refusals, fresh...)
		}
	}
	return refusals
}
