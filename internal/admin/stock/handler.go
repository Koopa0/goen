package stock

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/product/inventory"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/stockrisk"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("stock: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *admin.Control) {
	mux.HandleFunc("GET /admin/stock", ac.RequireStaff(h.Variants))
	mux.HandleFunc("GET /admin/stock/{sku}", ac.RequireStaff(h.Movements))
	mux.HandleFunc("POST /admin/stock/adjust", ac.RequireStaff(h.Adjust))
	mux.HandleFunc("POST /admin/stock/receive", ac.RequireStaff(h.Receive))
	mux.HandleFunc("POST /admin/stock/active", ac.RequireStaff(h.SetActive))
	mux.HandleFunc("POST /admin/stock/price", ac.RequireStaff(h.SetPrice))
	mux.HandleFunc("POST /admin/stock/arrival", ac.RequireStaff(h.SetArrival))
}

// listNotices are what a write made on the stock list can ask the list to say;
// skuNotices, what one made on a SKU page can ask that page to say. A code
// belongs to the page its write returns to and to no other.
var listNotices = map[string]widget.NoticeEntry{
	"refused":  widget.NoticeRefused(i18n.KeyAdminNoticeRefused),
	"received": widget.NoticeDone(i18n.KeyAdminNoticeReceived),
	"price":    widget.NoticeDone(i18n.KeyAdminNoticePriceSaved),
	"arrival":  widget.NoticeDone(i18n.KeyAdminNoticeArrivalSaved),
}

var skuNotices = map[string]widget.NoticeEntry{
	"refused":          widget.NoticeRefused(i18n.KeyAdminNoticeRefused),
	"received":         widget.NoticeDone(i18n.KeyAdminNoticeReceived),
	"receipt-conflict": widget.NoticeRefused(i18n.KeyAdminReceiptConflict),
	"adjusted":         widget.NoticeDone(i18n.KeyAdminNoticeStockAdjusted),
	"on":               widget.NoticeDone(i18n.KeyAdminNoticeVariantOn),
	"off":              widget.NoticeDone(i18n.KeyAdminNoticeVariantOff),
}

func (h *Handler) Variants(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Variants(r.Context(), r.URL.Query().Get("soldout") == "1", r.URL.Query().Get("q"), r.URL.Query().Get(keyset.Param))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read variants", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	if r.URL.Query().Get(keyset.Param) == "" && view.Term == "" && !view.SoldOutOnly {
		view.ShowCover = true
		now := time.Now()
		listed, more, coverErr := h.store.DaysCover(r.Context(), inventory.CoverWindowDays, now)
		if coverErr != nil {
			h.log.ErrorContext(r.Context(), "read days cover", "error", coverErr)
			admin.ServerError(w, r, h.log)
			return
		}
		view.AtRisk, view.MoreSoldOut = stockrisk.NewRows(listed, now), more
	}
	view.Notice = widget.NoticeFrom(r, listNotices)
	view.Return = stockReturn(r.URL.Query().Get("soldout"), view.Term, r.URL.Query().Get(keyset.Param), "", "")
	respond.Component(w, r, h.log, http.StatusOK, Variants(VariantsMeta(r.Context()), view))
}

// Adjust is posted from the SKU page, so it answers there: a correction is a
// count the reader is looking at, and its refusal needs the field it names.
func (h *Handler) Adjust(w http.ResponseWriter, r *http.Request) {
	u, _ := user.FromContext(r.Context())
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	delta, ok := ParseAdjustment(r.PostFormValue("delta"))
	if !ok {
		h.rejectAdjustment(w, r, i18n.KeyAdminStockDeltaError)
		return
	}
	key := r.PostFormValue("idempotency")
	if key == "" {
		key = newKey()
	}

	sku := r.PostFormValue("sku")
	back := "/admin/stock/" + url.PathEscape(sku)
	err := h.store.Adjust(r.Context(), sku, delta, u.ID, key)
	switch {
	case err == nil:
		http.Redirect(w, r, back+"?adjusted=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "stock adjustment refused",
			"sku", sku, "delta", delta, "error", err)
		h.rejectAdjustment(w, r, i18n.KeyAdminStockAdjustRefused)
	default:
		h.log.ErrorContext(r.Context(), "adjust stock", "error", err)
		admin.ServerError(w, r, h.log)
	}
}

func (h *Handler) rejectAdjustment(w http.ResponseWriter, r *http.Request, key i18n.Key) {
	h.rejectOnSKUPage(w, r, func(view *MovementsView) { h.renderAdjustmentRefusal(w, r, view, key) })
}

func (h *Handler) renderAdjustmentRefusal(w http.ResponseWriter, r *http.Request, view *MovementsView, key i18n.Key) {
	view.DraftDelta = r.PostFormValue("delta")
	view.DeltaError = i18n.T(r.Context(), key)
	view.AdjustmentKey = r.PostFormValue("idempotency")
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, Movements(
		frame.Page{Title: view.SKU}, view))
}

// rejectOnSKUPage reads the SKU page a refused form was posted from and hands
// it to render, which fills in the refusal. A SKU that is gone is a 404.
func (h *Handler) rejectOnSKUPage(w http.ResponseWriter, r *http.Request, render func(*MovementsView)) {
	view, err := h.store.Movements(r.Context(), r.PostFormValue("sku"), time.Now(), "")
	switch {
	case err == nil:
		render(&view)
	case errors.Is(err, ErrNotFound):
		admin.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "read movements after refused stock form", "error", err)
		admin.ServerError(w, r, h.log)
	}
}

func (h *Handler) rejectArrival(w http.ResponseWriter, r *http.Request) {
	h.rejectStockForm(w, r, i18n.KeyAdminVariantArrivalError, func(row *Variant) {
		row.ArrivalInput = r.PostFormValue("arrival_on")
		row.ArrivalError = i18n.T(r.Context(), i18n.KeyAdminVariantArrivalError)
	})
}

func (h *Handler) rejectStockForm(w http.ResponseWriter, r *http.Request, key i18n.Key, fill func(*Variant)) {
	var soldOut, term, after string
	if u, err := url.Parse(r.PostFormValue("return")); err == nil && u.Path == "/admin/stock" && u.Host == "" {
		soldOut, term, after = u.Query().Get("soldout"), u.Query().Get("q"), u.Query().Get(keyset.Param)
	}
	view, err := h.store.Variants(r.Context(), soldOut == "1", term, after)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read variants after refused stock form", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	view.Return = stockReturn(soldOut, view.Term, after, "", "")
	sku := r.PostFormValue("sku")
	shown := false
	for i := range view.Variants {
		if view.Variants[i].SKU == sku {
			fill(&view.Variants[i])
			shown = true
		}
	}
	if !shown {
		// The row is not on this page, so the banner has to say it.
		view.Notice = widget.Result{Outcome: widget.OutcomeRefused, Text: i18n.T(r.Context(), key)}
	}
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, Variants(VariantsMeta(r.Context()), view))
}

// Receive books a delivery from a row of the stock list, which names the list
// in "return", or from the SKU page, which names nothing. The list answers on
// the row it came from; the SKU page keeps its own notices.
func (h *Handler) Receive(w http.ResponseWriter, r *http.Request) {
	u, _ := user.FromContext(r.Context())
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	sku := r.PostFormValue("sku")
	back := "/admin/stock/" + url.PathEscape(sku)
	fromList := postedFromStockList(r)
	key := r.PostFormValue("idempotency")
	if key == "" {
		key = newKey()
		r.PostForm.Set("idempotency", key)
	}

	quantity, ok := ParseReceipt(r.PostFormValue("quantity"))
	if !ok {
		h.rejectReceipt(w, r, fromList, i18n.KeyAdminNoticeBadQty)
		return
	}

	err := h.store.Receive(r.Context(), sku, quantity, u.ID, key)
	switch {
	case err == nil && fromList:
		http.Redirect(w, r, stockBack(r, "received"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
	case err == nil:
		http.Redirect(w, r, back+"?received=1", http.StatusSeeOther)
	case errors.Is(err, ErrMovementConflict) && fromList:
		h.rejectReceipt(w, r, fromList, i18n.KeyAdminReceiptConflict)
	case errors.Is(err, ErrMovementConflict):
		http.Redirect(w, r, back+"?receipt-conflict=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "goods receipt refused",
			"sku", sku, "quantity", quantity, "error", err)
		if fromList {
			h.rejectReceipt(w, r, fromList, i18n.KeyAdminNoticeRefused)
			return
		}
		http.Redirect(w, r, back+"?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "receive stock", "error", err)
		admin.ServerError(w, r, h.log)
	}
}

func (h *Handler) rejectReceipt(w http.ResponseWriter, r *http.Request, fromList bool, key i18n.Key) {
	if fromList {
		h.rejectStockForm(w, r, key, func(row *Variant) {
			row.DraftQuantity = r.PostFormValue("quantity")
			row.QuantityError = i18n.T(r.Context(), key)
		})
		return
	}
	h.rejectOnSKUPage(w, r, func(view *MovementsView) { h.renderReceiptRefusal(w, r, view, key) })
}

func (h *Handler) renderReceiptRefusal(w http.ResponseWriter, r *http.Request, view *MovementsView, key i18n.Key) {
	view.DraftQuantity = r.PostFormValue("quantity")
	view.QuantityError = i18n.T(r.Context(), key)
	view.ReceiptKey = r.PostFormValue("idempotency")
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, Movements(
		frame.Page{Title: view.SKU}, view))
}

// SetActive is posted from the SKU page and answers there, where its notice is
// on screen and the variant's state is the one being read.
func (h *Handler) SetActive(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	sku := r.PostFormValue("sku")
	back := "/admin/stock/" + url.PathEscape(sku)
	active := r.PostFormValue("active") == "1"
	err := h.store.SetActive(r.Context(), sku, active)
	switch {
	case err == nil && active:
		http.Redirect(w, r, back+"?on=1", http.StatusSeeOther)
	case err == nil:
		http.Redirect(w, r, back+"?off=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "variant activation refused",
			"sku", sku, "error", err)
		http.Redirect(w, r, back+"?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set variant active", "error", err)
		admin.ServerError(w, r, h.log)
	}
}

func (h *Handler) SetPrice(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	price, okPrice := money.ParseDollars(r.PostFormValue("price"))
	compare, okCompare := money.ParseDollars(r.PostFormValue("compare_at"))
	if !okPrice || !okCompare || price <= 0 {
		http.Redirect(w, r, stockBack(r, "refused"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
		return
	}

	err := h.store.SetPrice(r.Context(), r.PostFormValue("sku"), price, compare)
	switch {
	case err == nil:
		http.Redirect(w, r, stockBack(r, "price"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "reprice refused",
			"sku", r.PostFormValue("sku"), "error", err)
		http.Redirect(w, r, stockBack(r, "refused"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
	default:
		h.log.ErrorContext(r.Context(), "set variant price", "error", err)
		admin.ServerError(w, r, h.log)
	}
}

// postedFromStockList reports whether the form named the stock list as the page
// it was posted from. The value is only compared, never followed.
func postedFromStockList(r *http.Request) bool {
	u, err := url.Parse(r.PostFormValue("return"))
	return err == nil && u.Path == "/admin/stock" && u.Host == ""
}

func stockReturn(soldOut, term, after, notice, sku string) string {
	q := url.Values{}
	if soldOut == "1" {
		q.Set("soldout", "1")
	}
	if term != "" {
		q.Set("q", term)
	}
	if after != "" {
		q.Set(keyset.Param, after)
	}
	if notice != "" {
		q.Set(notice, "1")
	}
	target := "/admin/stock"
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	if sku != "" {
		target += "#row-" + url.PathEscape(sku)
	}
	return target
}

// stockBack is where a write on the stock list returns to: the filter and page
// the form was posted from, with the notice and the row anchored. The posted
// value is only ever read for those two parameters, so it cannot name another
// address.
func stockBack(r *http.Request, notice string) string {
	var soldOut, term, after string
	if u, err := url.Parse(r.PostFormValue("return")); err == nil && u.Path == "/admin/stock" && u.Host == "" {
		soldOut, term, after = u.Query().Get("soldout"), formtext.SearchTerm(u.Query().Get("q")), u.Query().Get(keyset.Param)
	}
	return stockReturn(soldOut, term, after, notice, r.PostFormValue("sku"))
}

func newKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (h *Handler) Movements(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Movements(r.Context(), r.PathValue("sku"), time.Now(), r.URL.Query().Get(keyset.Param))
	switch {
	case err == nil:
		view.Notice = widget.NoticeFrom(r, skuNotices)
		respond.Component(w, r, h.log, http.StatusOK, Movements(
			frame.Page{Title: view.SKU}, &view))
	case errors.Is(err, ErrNotFound):
		admin.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "read movements", "error", err)
		admin.ServerError(w, r, h.log)
	}
}

func (h *Handler) SetArrival(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	day, ok := ParseArrival(r.PostFormValue("arrival_on"))
	if !ok {
		h.rejectArrival(w, r)
		return
	}
	err := h.store.SetArrival(r.Context(), r.PostFormValue("sku"), day)
	switch {
	case err == nil:
		http.Redirect(w, r, stockBack(r, "arrival"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
	case errors.Is(err, ErrNotFound):
		admin.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "set variant arrival", "error", err)
		admin.ServerError(w, r, h.log)
	}
}
