package loyalty

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("loyalty: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/tiers", ac.RequireStaff(h.Tiers))
	mux.HandleFunc("POST /admin/tiers", ac.RequireStaff(h.CreateTier))
	mux.HandleFunc("POST /admin/tiers/delete", ac.RequireStaff(h.DeleteTier))
	mux.HandleFunc("GET /admin/credit", ac.RequireStaff(h.Credit))
	mux.HandleFunc("POST /admin/credit", ac.RequireStaff(h.GrantCredit))
}

var notices = map[string]web.NoticeEntry{
	"ok":          web.Done(i18n.KeyAdminNoticeOK),
	"refused":     web.Refused(i18n.KeyAdminNoticeRefused),
	"creditneeds": web.Refused(i18n.KeyAdminNoticeCreditNeeds),
	"tiersneeds":  web.Refused(i18n.KeyAdminNoticeTiersNeeds),
}

func (h *Handler) Credit(w http.ResponseWriter, r *http.Request) {
	var view admin.CreditView
	var err error
	if customer := r.URL.Query().Get("customer"); customer != "" {
		view, err = h.store.CreditForCustomer(r.Context(), customer, r.URL.Query().Get(web.KeysetParam))
	} else {
		view, err = h.store.Credit(r.Context(), r.URL.Query().Get(web.KeysetParam))
	}
	if errors.Is(err, ErrNotFound) {
		access.NotFound(w, r, h.log)
		return
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "read credit ledger", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.OperationID = uuid.NewString()
	view.Notice = creditNotice(r)
	web.Render(w, r, h.log, http.StatusOK, admin.Credit(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCredit)}, view))
}

// creditNotice is noticeFor plus the BALANCE a grant produced: the form is a
// blank box, so a grant nothing confirms is one somebody makes twice.
func creditNotice(r *http.Request) components.Result {
	if r.URL.Query().Get("ok") != "1" {
		return web.Notice(r, notices)
	}
	balance, err := strconv.ParseInt(r.URL.Query().Get("balance"), 10, 64)
	if err != nil {
		return web.Notice(r, notices)
	}
	return components.Result{
		Outcome: components.OutcomeDone,
		Text:    fmt.Sprintf(i18n.T(r.Context(), i18n.KeyAdminNoticeCreditGranted), money.TWD(balance)),
	}
}

// GrantCredit serves POST /admin/credit. The amount is typed in DOLLARS and
// stored in cents.
func (h *Handler) GrantCredit(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	view := admin.CreditView{Email: r.PostFormValue("email"), Amount: r.PostFormValue("amount"), Reason: r.PostFormValue("reason"), OperationID: r.PostFormValue("operation_id"), FilterCustomerID: r.PostFormValue("filter_customer")}
	operationID, valid := validateCreditGrant(&view)
	if !valid {
		h.renderCreditForm(w, r, &view, http.StatusUnprocessableEntity, "")
		return
	}
	if err := h.store.creditRecipient(r.Context(), &view); err != nil {
		if errors.Is(err, ErrNotFound) {
			view.EmailInvalid = true
			h.renderCreditForm(w, r, &view, http.StatusUnprocessableEntity, i18n.KeyAdminCreditUnknown)
		} else {
			h.log.ErrorContext(r.Context(), "read credit recipient", "error", err)
			access.ServerError(w, r, h.log)
		}
		return
	}
	if view.FilterCustomerID != view.CustomerID {
		view.FilterCustomerID = ""
	}
	if r.PostFormValue("edit") == "1" {
		h.renderCreditForm(w, r, &view, http.StatusOK, "")
		return
	}
	if r.PostFormValue("confirm") != "grant" || r.PostFormValue("customer_id") != view.CustomerID {
		view.Confirm = true
		h.renderCreditForm(w, r, &view, http.StatusOK, "")
		return
	}
	customerID, parseErr := uuid.Parse(view.CustomerID)
	if parseErr != nil {
		access.ServerError(w, r, h.log)
		return
	}
	balance, err := h.store.GrantCredit(r.Context(), customerID, view.GrantCents, view.Reason, operationID)
	switch {
	case err == nil:
		// The balance travels as a number and never the address it belongs to,
		// because a query string is logged.
		target := "/admin/credit?ok=1&balance=" + strconv.FormatInt(balance, 10)
		if view.FilterCustomerID != "" {
			target = web.ScopeURL("/admin/credit", "ok", "1", "balance", strconv.FormatInt(balance, 10), "customer", view.FilterCustomerID)
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	case errors.Is(err, ErrInvalid):
		http.Redirect(w, r, "/admin/credit?creditneeds=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "credit grant refused", "error", err)
		http.Redirect(w, r, "/admin/credit?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "grant credit", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) Tiers(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Tiers(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read membership tiers", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = web.Notice(r, notices)
	web.Render(w, r, h.log, http.StatusOK, admin.Tiers(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageTiers)}, view))
}

func (h *Handler) CreateTier(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	threshold, tErr := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("threshold")), 10, 64)
	percent, pErr := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("percent")), 10, 64)
	if tErr != nil || pErr != nil {
		http.Redirect(w, r, "/admin/tiers?tiersneeds=1", http.StatusSeeOther)
		return
	}
	err := h.store.CreateTier(r.Context(), r.PostFormValue("code"),
		r.PostFormValue("name"), r.PostFormValue("name_en"), threshold, percent)
	h.redirectTiers(w, r, err)
}

func (h *Handler) DeleteTier(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	h.redirectTiers(w, r, h.store.DeleteTier(r.Context(), r.PostFormValue("tier")))
}

func (h *Handler) redirectTiers(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/tiers?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrNotFound):
		http.Redirect(w, r, "/admin/tiers?tiersneeds=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "tier change refused", "error", err)
		http.Redirect(w, r, "/admin/tiers?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "change membership tiers", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) renderCreditForm(w http.ResponseWriter, r *http.Request, view *admin.CreditView, status int, notice i18n.Key) {
	ledger, err := h.store.CreditForCustomer(r.Context(), view.FilterCustomerID)
	if errors.Is(err, ErrNotFound) {
		access.NotFound(w, r, h.log)
		return
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "read credit ledger", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Rows, view.Bound = ledger.Rows, ledger.Bound
	view.FilterCustomerID, view.FilterCustomerName = ledger.FilterCustomerID, ledger.FilterCustomerName
	if notice != "" {
		view.Notice = components.Result{Outcome: components.OutcomeRefused, Text: i18n.T(r.Context(), notice)}
	}
	web.Render(w, r, h.log, status, admin.Credit(layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCredit)}, *view))
}
