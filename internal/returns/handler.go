package returns

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// OrderAccess is the one thing this package needs from internal/cart: whether the
// browser making this request holds a token for the order it is asking about.
//
// Defined here, by the consumer. internal/cart returns its concrete *Store and knows
// nothing about this interface.
type OrderAccess interface {
	PlacedHere(ctx context.Context, r *http.Request, number string, secure bool) bool
}

// Handler serves the customer's return form.
type Handler struct {
	access OrderAccess
	store  *Store
	log    *slog.Logger
	secure bool
}

// NewHandler wires the return routes.
func NewHandler(s *Store, access OrderAccess, log *slog.Logger, secureCookies bool) *Handler {
	if s == nil || access == nil || log == nil {
		panic("returns: NewHandler requires a store, an access check and a logger")
	}
	return &Handler{store: s, access: access, log: log, secure: secureCookies}
}

// Page shows what may be sent back.
//
// GET /orders/{number}/return
func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	o, ok := h.ownOrder(w, r)
	if !ok {
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Returns(
		pages.ReturnsMeta(r.Context(), o.Number), viewOf(r.Context(), o, nil, "")))
}

// Submit files the request.
//
// POST /orders/{number}/return
//
// A plain form. Each returnable line carries a number input named for its own
// id, so the request is fully expressed in the body and nothing about it needs
// scripting.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	o, ok := h.ownOrder(w, r)
	if !ok {
		return
	}

	req := &Request{Reason: r.PostFormValue("reason"), Lines: map[string]int32{}}
	for i := range o.Lines {
		l := &o.Lines[i]
		raw := r.PostFormValue("qty_" + l.ID)
		if raw == "" {
			continue
		}
		// Bounded parse. An unparseable or negative quantity is the form being
		// hand-edited, and Validate refuses it — but a bound here keeps a
		// twenty-digit number out of the int32 conversion entirely.
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n < 0 || n > int64(l.Returnable) {
			h.reject(w, r, o, req, i18n.T(r.Context(), i18n.KeyReturnTooMany))
			return
		}
		req.Lines[l.ID] = int32(n)
	}

	var userID uuid.NullUUID
	if u, signedIn := account.FromContext(r.Context()); signedIn {
		if id, parseErr := uuid.Parse(u.ID); parseErr == nil {
			userID = uuid.NullUUID{UUID: id, Valid: true}
		}
	}

	err := h.store.Open(r.Context(), o.Number, userID, req)
	switch {
	case err == nil:
		// 303, so a reload cannot file a second request. o.Number came back
		// from the database, so it matches orders_number_format and cannot
		// steer the redirect.
		http.Redirect(w, r, "/orders/"+o.Number+"/return?filed=1", http.StatusSeeOther)
	case errors.Is(err, ErrAlreadyOpen):
		h.reject(w, r, o, req, i18n.T(r.Context(), i18n.KeyReturnAlreadyOpen))
	case errors.Is(err, ErrNotReturnable):
		h.reject(w, r, o, req, i18n.T(r.Context(), i18n.KeyReturnNothingShort))
	case errors.Is(err, ErrInvalid):
		h.reject(w, r, o, req, i18n.T(r.Context(), i18n.KeyReturnNeedsReason))
	default:
		h.log.ErrorContext(r.Context(), "open return request", "order", o.Number, "error", err)
		web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
	}
}

// reject re-renders the form at 422 with what the customer typed still in it.
func (h *Handler) reject(w http.ResponseWriter, r *http.Request, o *Order, req *Request, msg string) {
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.Returns(
		pages.ReturnsMeta(r.Context(), o.Number), viewOf(r.Context(), o, req, msg)))
}

// ownOrder loads an order the requester may act on, or writes the refusal.
//
// The same gate as the confirmation and payment pages: an order number is a
// per-day counter, so knowing one is not authorisation. A stranger gets the 404
// an absent order gets.
func (h *Handler) ownOrder(w http.ResponseWriter, r *http.Request) (*Order, bool) {
	number := r.PathValue("number")
	if !h.access.PlacedHere(r.Context(), r, number, h.secure) && !h.ownedBySignedInUser(r, number) {
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

func (h *Handler) ownedBySignedInUser(r *http.Request, number string) bool {
	u, ok := account.FromContext(r.Context())
	if !ok {
		return false
	}
	owns, err := h.store.OrderBelongsTo(r.Context(), number, u.ID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "check order ownership", "error", err)
		return false
	}
	return owns
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyOrderNotFound)}, "404",
		i18n.T(r.Context(), i18n.KeyOrderNotFound),
		i18n.T(r.Context(), i18n.KeyOrderNotYours)))
}

// viewOf assembles the page. req is nil on a first render and carries the
// submitted values when the form is being shown again after a refusal.
func viewOf(ctx context.Context, o *Order, req *Request, errMsg string) pages.ReturnsView {
	v := pages.ReturnsView{
		Number: o.Number, HasOpen: o.HasOpen, Error: errMsg,
	}
	for i := range o.Lines {
		l := &o.Lines[i]
		line := pages.ReturnsLine{
			ID: l.ID, SKU: l.SKU, Name: l.Name, Label: l.Label,
			UnitCents: l.UnitCents, Returnable: l.Returnable,
		}
		if req != nil {
			line.Chosen = req.Lines[l.ID]
		}
		v.Lines = append(v.Lines, line)
	}
	if req != nil {
		v.Reason = req.Reason
	}
	for i := range o.Existing {
		e := &o.Existing[i]
		v.Existing = append(v.Existing, pages.ReturnsExisting{
			StatusText: StatusLabel(ctx, e.Status), Reason: e.Reason,
			Resolution: e.Resolution, CreatedAt: e.CreatedAt, DecidedAt: e.DecidedAt,
		})
	}
	return v
}
