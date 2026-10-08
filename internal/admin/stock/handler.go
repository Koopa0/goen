package stock

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
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

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/stock", ac.RequireStaff(h.Variants))
	mux.HandleFunc("GET /admin/stock/{sku}", ac.RequireStaff(h.Movements))
	mux.HandleFunc("POST /admin/stock/adjust", ac.RequireStaff(h.Adjust))
	mux.HandleFunc("POST /admin/stock/receive", ac.RequireStaff(h.Receive))
	mux.HandleFunc("POST /admin/stock/active", ac.RequireStaff(h.SetActive))
	mux.HandleFunc("POST /admin/stock/price", ac.RequireStaff(h.SetPrice))
	mux.HandleFunc("POST /admin/stock/arrival", ac.RequireStaff(h.SetArrival))
}

var notices = map[string]web.NoticeEntry{
	"ok":               web.Done(i18n.KeyAdminNoticeOK),
	"refused":          web.Refused(i18n.KeyAdminNoticeRefused),
	"received":         web.Done(i18n.KeyAdminNoticeReceived),
	"receipt-conflict": web.Refused(i18n.KeyAdminReceiptConflict),
}

func (h *Handler) Variants(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Variants(r.Context(), r.URL.Query().Get("soldout") == "1", r.URL.Query().Get("q"), r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read variants", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	if r.URL.Query().Get(web.KeysetParam) == "" && view.Term == "" && !view.SoldOutOnly {
		view.ShowCover = true
		view.AtRisk, view.MoreSoldOut, err = h.store.DaysCover(r.Context(), admin.CoverWindowDays, time.Now())
		if err != nil {
			h.log.ErrorContext(r.Context(), "read days cover", "error", err)
			access.ServerError(w, r, h.log)
			return
		}
	}
	view.Notice = web.Notice(r, notices)
	view.Return = stockReturn(r.URL.Query().Get("soldout"), view.Term, r.URL.Query().Get(web.KeysetParam), "", "")
	web.Render(w, r, h.log, http.StatusOK, admin.Variants(admin.VariantsMeta(r.Context()), view))
}

func (h *Handler) Adjust(w http.ResponseWriter, r *http.Request) {
	u, _ := user.FromContext(r.Context())
	if err := web.ParseForm(w, r); err != nil {
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

	err := h.store.Adjust(r.Context(), r.PostFormValue("sku"), delta, u.ID, key)
	switch {
	case err == nil:
		http.Redirect(w, r, stockBack(r, "ok"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "stock adjustment refused",
			"sku", r.PostFormValue("sku"), "delta", delta, "error", err)
		h.rejectAdjustment(w, r, i18n.KeyAdminStockAdjustRefused)
	default:
		h.log.ErrorContext(r.Context(), "adjust stock", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) rejectAdjustment(w http.ResponseWriter, r *http.Request, key i18n.Key) {
	h.rejectStockForm(w, r, key, func(row *admin.Variant) {
		row.DraftDelta = r.PostFormValue("delta")
		row.DeltaError = i18n.T(r.Context(), key)
	})
}

func (h *Handler) rejectArrival(w http.ResponseWriter, r *http.Request) {
	h.rejectStockForm(w, r, i18n.KeyAdminVariantArrivalError, func(row *admin.Variant) {
		row.ArrivalInput = r.PostFormValue("arrival_on")
		row.ArrivalError = i18n.T(r.Context(), i18n.KeyAdminVariantArrivalError)
	})
}

func (h *Handler) rejectStockForm(w http.ResponseWriter, r *http.Request, key i18n.Key, fill func(*admin.Variant)) {
	var soldOut, term, after string
	if u, err := url.Parse(r.PostFormValue("return")); err == nil && u.Path == "/admin/stock" && u.Host == "" {
		soldOut, term, after = u.Query().Get("soldout"), u.Query().Get("q"), u.Query().Get(web.KeysetParam)
	}
	view, err := h.store.Variants(r.Context(), soldOut == "1", term, after)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read variants after refused stock form", "error", err)
		access.ServerError(w, r, h.log)
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
		view.Notice = components.Result{Outcome: components.OutcomeRefused, Text: i18n.T(r.Context(), key)}
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Variants(admin.VariantsMeta(r.Context()), view))
}

func (h *Handler) Receive(w http.ResponseWriter, r *http.Request) {
	u, _ := user.FromContext(r.Context())
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	sku := r.PostFormValue("sku")
	back := "/admin/stock/" + url.PathEscape(sku)
	key := r.PostFormValue("idempotency")
	if key == "" {
		key = newKey()
		r.PostForm.Set("idempotency", key)
	}

	quantity, ok := ParseReceipt(r.PostFormValue("quantity"))
	if !ok {
		h.rejectReceipt(w, r, i18n.KeyAdminNoticeBadQty)
		return
	}

	err := h.store.Receive(r.Context(), sku, quantity, u.ID, key)
	switch {
	case err == nil:
		http.Redirect(w, r, back+"?received=1", http.StatusSeeOther)
	case errors.Is(err, ErrMovementConflict):
		http.Redirect(w, r, back+"?receipt-conflict=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "goods receipt refused",
			"sku", sku, "quantity", quantity, "error", err)
		h.rejectReceipt(w, r, i18n.KeyAdminNoticeRefused)
	default:
		h.log.ErrorContext(r.Context(), "receive stock", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) rejectReceipt(w http.ResponseWriter, r *http.Request, key i18n.Key) {
	view, err := h.store.Movements(r.Context(), r.PostFormValue("sku"), time.Now())
	switch {
	case err == nil:
		h.renderReceiptRefusal(w, r, &view, key)
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "read movements after refused receipt", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) renderReceiptRefusal(w http.ResponseWriter, r *http.Request, view *admin.MovementsView, key i18n.Key) {
	view.DraftQuantity = r.PostFormValue("quantity")
	view.QuantityError = i18n.T(r.Context(), key)
	view.ReceiptKey = r.PostFormValue("idempotency")
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Movements(
		layouts.Page{Title: view.SKU}, view))
}

func (h *Handler) SetActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.SetActive(r.Context(),
		r.PostFormValue("sku"), r.PostFormValue("active") == "1")
	switch {
	case err == nil:
		http.Redirect(w, r, stockBack(r, "ok"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "variant activation refused",
			"sku", r.PostFormValue("sku"), "error", err)
		http.Redirect(w, r, stockBack(r, "refused"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
	default:
		h.log.ErrorContext(r.Context(), "set variant active", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) SetPrice(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
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
		http.Redirect(w, r, stockBack(r, "ok"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "reprice refused",
			"sku", r.PostFormValue("sku"), "error", err)
		http.Redirect(w, r, stockBack(r, "refused"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
	default:
		h.log.ErrorContext(r.Context(), "set variant price", "error", err)
		access.ServerError(w, r, h.log)
	}
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
		q.Set(web.KeysetParam, after)
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
		soldOut, term, after = u.Query().Get("soldout"), web.SearchTerm(u.Query().Get("q")), u.Query().Get(web.KeysetParam)
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
	view, err := h.store.Movements(r.Context(), r.PathValue("sku"), time.Now(), r.URL.Query().Get(web.KeysetParam))
	switch {
	case err == nil:
		view.Notice = web.Notice(r, notices)
		web.Render(w, r, h.log, http.StatusOK, admin.Movements(
			layouts.Page{Title: view.SKU}, &view))
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "read movements", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) SetArrival(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
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
		http.Redirect(w, r, stockBack(r, "ok"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "set variant arrival", "error", err)
		access.ServerError(w, r, h.log)
	}
}
