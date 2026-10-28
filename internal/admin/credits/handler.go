package credits

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/credit"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("credits: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *admin.Control) {
	mux.HandleFunc("GET /admin/credits", ac.RequireStaff(h.Credit))
	mux.HandleFunc("POST /admin/credits", ac.RequireStaff(h.GrantCredit))
}

var notices = map[string]widget.NoticeEntry{
	"refused":     widget.NoticeRefused(i18n.KeyAdminNoticeRefused),
	"creditneeds": widget.NoticeRefused(i18n.KeyAdminNoticeCreditNeeds),
}

func (h *Handler) Credit(w http.ResponseWriter, r *http.Request) {
	customer, ok := creditFilter(r.URL.Query().Get("customer"))
	if !ok {
		admin.NotFound(w, r, h.log)
		return
	}
	ledger, err := h.store.Credit(r.Context(), customer, r.URL.Query().Get(keyset.Param))
	if errors.Is(err, ErrNotFound) {
		admin.NotFound(w, r, h.log)
		return
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "read credit ledger", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	view := creditView(ledger)
	// On one customer's page the grant form is already addressed to them.
	if ledger.Customer != nil {
		view.Email = ledger.Customer.Email
	}
	view.OperationID = uuid.NewString()
	view.Notice = widget.NoticeFrom(r, notices)
	// Only on the page of the customer a grant went to, and from the ledger's
	// own balance now: the address carries no number, so a reload never states
	// a stale one and a made-up link cannot state a false one.
	if holder, asked := grantedHolder(r, ledger); asked {
		balance, balanceErr := h.store.Balance(r.Context(), holder)
		if balanceErr != nil {
			h.log.ErrorContext(r.Context(), "read credit balance", "error", balanceErr)
			admin.ServerError(w, r, h.log)
			return
		}
		view.Notice = grantedNotice(r.Context(), balance)
	}
	respond.Component(w, r, h.log, http.StatusOK, Credit(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCredit)}, view))
}

// creditFilter reads the customer a page is narrowed to. Absent is every
// customer; anything that is not a customer's identifier is not ok.
func creditFilter(raw string) (customer uuid.NullUUID, ok bool) {
	if raw == "" {
		return uuid.NullUUID{}, true
	}
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.NullUUID{}, false
	}
	return uuid.NullUUID{UUID: id, Valid: true}, true
}

// creditView puts a ledger page in the words the page renders.
func creditView(ledger Ledger) View {
	view := View{Bound: ledger.Bound}
	if ledger.Customer != nil {
		view.FilterCustomerID = ledger.Customer.ID.String()
		view.FilterCustomerName = ledger.Customer.Name
		if view.FilterCustomerName == "" {
			view.FilterCustomerName = ledger.Customer.Email
		}
	}
	for i := range ledger.Postings {
		p := &ledger.Postings[i]
		view.Rows = append(view.Rows, Entry{
			Email: p.Email, AmountCents: p.AmountCents, Kind: credit.Kind(p.Kind), Reason: p.Reason,
			At: shoptime.Minute(p.At), CustomerID: p.CustomerID, OrderNumber: p.OrderNumber,
			ReturnID: p.ReturnID, ActorEmail: p.ActorEmail, RunningTotalCents: p.RunningCents,
		})
	}
	return view
}

// grantedHolder is whose balance the page is asked to state. The answer is a
// customer the ledger found, so a link naming one that does not exist, or none,
// states nothing and never a balance of zero.
func grantedHolder(r *http.Request, ledger Ledger) (customer uuid.UUID, asked bool) {
	if ledger.Customer == nil || r.URL.Query().Get("granted") != "1" {
		return uuid.Nil, false
	}
	return ledger.Customer.ID, true
}

// grantedNotice is the sentence after a grant: what the customer's balance is.
// The form is a blank box, so a grant nothing confirms is one somebody makes twice.
func grantedNotice(ctx context.Context, balanceCents int64) widget.Result {
	return widget.Result{
		Outcome: widget.OutcomeDone,
		Text:    fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminNoticeCreditGranted), money.TWD(balanceCents)),
	}
}

// GrantCredit serves POST /admin/credits. The amount is typed in DOLLARS and
// stored in cents.
func (h *Handler) GrantCredit(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	view := View{Email: r.PostFormValue("email"), Amount: r.PostFormValue("amount"), Reason: r.PostFormValue("reason"), OperationID: r.PostFormValue("operation_id"), FilterCustomerID: r.PostFormValue("filter_customer")}
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
			admin.ServerError(w, r, h.log)
		}
		return
	}
	// A grant to someone else than the customer being viewed leaves that page.
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
		admin.ServerError(w, r, h.log)
		return
	}
	err := h.store.GrantCredit(r.Context(), customerID, view.GrantCents, view.Reason, operationID)
	switch {
	case err == nil:
		// Back to the ledger of whoever was paid, whose page names them and
		// lists this grant first. Nothing else travels: no amount and no
		// balance, which the page reads when it shows.
		http.Redirect(w, r, "/admin/credits?customer="+customerID.String()+"&granted=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid):
		http.Redirect(w, r, "/admin/credits?creditneeds=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "credit grant refused", "error", err)
		http.Redirect(w, r, "/admin/credits?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "grant credit", "error", err)
		admin.ServerError(w, r, h.log)
	}
}

func (h *Handler) renderCreditForm(w http.ResponseWriter, r *http.Request, view *View, status int, notice i18n.Key) {
	customer, ok := creditFilter(view.FilterCustomerID)
	if !ok {
		admin.NotFound(w, r, h.log)
		return
	}
	ledger, err := h.store.Credit(r.Context(), customer, "")
	if errors.Is(err, ErrNotFound) {
		admin.NotFound(w, r, h.log)
		return
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "read credit ledger", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	shown := creditView(ledger)
	view.Rows, view.Bound = shown.Rows, shown.Bound
	view.FilterCustomerID, view.FilterCustomerName = shown.FilterCustomerID, shown.FilterCustomerName
	if notice != "" {
		view.Notice = widget.Result{Outcome: widget.OutcomeRefused, Text: i18n.T(r.Context(), notice)}
	}
	respond.Component(w, r, h.log, status, Credit(frame.Page{Refused: status == http.StatusUnprocessableEntity, Title: i18n.T(r.Context(), i18n.KeyAdminPageCredit)}, *view))
}
