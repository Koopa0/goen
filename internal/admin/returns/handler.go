package returns

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/refundstate"
	returnrules "github.com/koopa0/goen/internal/returns"
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
		panic("returns: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/returns", ac.RequireStaff(h.Queue))
	mux.HandleFunc("POST /admin/returns/{id}/decide", ac.RequireStaff(h.Decide))
	mux.HandleFunc("POST /admin/returns/{id}/assess", ac.RequireStaff(h.Assess))
	mux.HandleFunc("POST /admin/returns/{id}/inspect", ac.RequireStaff(h.Inspect))
	mux.HandleFunc("POST /admin/returns/{id}/complete", ac.RequireStaff(h.Complete))
}

var notices = map[string]web.NoticeEntry{
	"ok":           web.Done(i18n.KeyAdminNoticeOK),
	"refused":      web.Refused(i18n.KeyAdminNoticeRefused),
	"refundfailed": web.Failed(i18n.KeyAdminNoticeRefundFailed),
	"assessed":     web.Done(i18n.KeyAdminNoticeAssessed),
	"inspected":    web.Done(i18n.KeyAdminNoticeInspected),
	"closed":       web.Done(i18n.KeyAdminNoticeClosed),
}

func (h *Handler) Queue(w http.ResponseWriter, r *http.Request) {
	queue, err := h.store.Queue(r.Context(), r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read return queue", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	for _, issue := range queue.payoutIssues {
		h.log.ErrorContext(r.Context(), "return payout no longer fits its sources",
			"return_id", issue.returnID, "error", issue.err)
	}
	view := admin.ReturnsView{
		Bound:  queue.Bound,
		Rows:   queue.Rows,
		Notice: web.Notice(r, notices),
	}
	web.Render(w, r, h.log, http.StatusOK, admin.Returns(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageReturns)}, view))
}

// Decide approves or rejects a return. Approving pays money back, so a refusal
// from the database or from Stripe is reported and never swallowed.
func (h *Handler) Decide(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	if h.confirmReturnDecision(w, r) {
		return
	}
	err := h.store.Decide(r.Context(), r.PathValue("id"),
		r.PostFormValue("decision"), r.PostFormValue("resolution"),
		r.PostFormValue("assessment_version"))
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/returns?ok=1", http.StatusSeeOther)
	case errors.Is(err, refundstate.ErrIncomplete):
		// A payout may preserve a database refusal as its cause, but once approval
		// committed the operator needs the recovery notice, not the generic
		// "decision refused" notice. Test this before ErrRefused.
		h.log.ErrorContext(r.Context(), "decide return",
			"return", r.PathValue("id"), "error", err)
		http.Redirect(w, r, "/admin/returns?refundfailed=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrRefused), errors.Is(err, refundstate.ErrRefused):
		h.log.WarnContext(r.Context(), "return decision refused",
			"return", r.PathValue("id"), "error", err)
		if h.renderReturnRefusal(w, r, err) {
			return
		}
		http.Redirect(w, r, "/admin/returns?refused=1", http.StatusSeeOther)
	default:
		// Infrastructure errors which occurred before this became a durable payout
		// recovery land here.
		h.log.ErrorContext(r.Context(), "decide return",
			"return", r.PathValue("id"), "error", err)
		access.ServerError(w, r, h.log)
	}
}

// Assess records eligibility facts and never pays or restocks.
func (h *Handler) Assess(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	facts, parseErr := assessmentLines(r)
	if parseErr != nil {
		h.log.WarnContext(r.Context(), "return assessment rejected",
			"return", r.PathValue("id"), "error", parseErr)
		if h.renderReturnRefusal(w, r, formRefuse("decision", returnrules.RefuseIncomplete)) {
			return
		}
		http.Redirect(w, r, "/admin/returns?refused=1", http.StatusSeeOther)
		return
	}
	err := h.store.Assess(r.Context(), r.PathValue("id"), r.PostFormValue("basis"), facts)
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/returns?assessed=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "return assessment refused",
			"return", r.PathValue("id"), "error", err)
		if h.renderReturnRefusal(w, r, err) {
			return
		}
		http.Redirect(w, r, "/admin/returns?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "assess return",
			"return", r.PathValue("id"), "error", err)
		access.ServerError(w, r, h.log)
	}
}

func assessmentLines(r *http.Request) ([]LineEligibility, error) {
	seen := map[string]LineEligibility{}
	for name, values := range r.PostForm {
		kind, rest, ok := strings.Cut(name, "_")
		if !ok || len(values) == 0 {
			continue
		}
		var dest *string
		switch kind {
		case "unused", "packaging", "accessories":
		default:
			continue
		}
		lineID, err := uuid.Parse(rest)
		if err != nil {
			return nil, fmt.Errorf("field %q does not name an order line: %w", name, err)
		}
		fact, ok := returnrules.ParseFact(strings.TrimSpace(values[0]))
		if !ok {
			return nil, fmt.Errorf("field %q is not a known fact", name)
		}
		row := seen[rest]
		row.OrderLineID = lineID
		switch kind {
		case "unused":
			dest = &row.Unused
		case "packaging":
			dest = &row.Packaging
		case "accessories":
			dest = &row.Accessories
		}
		*dest = string(fact)
		seen[rest] = row
	}
	out := make([]LineEligibility, 0, len(seen))
	for _, row := range seen {
		if row.Unused == "" {
			row.Unused = string(returnrules.FactUnknown)
		}
		if row.Packaging == "" {
			row.Packaging = string(returnrules.FactUnknown)
		}
		if row.Accessories == "" {
			row.Accessories = string(returnrules.FactUnknown)
		}
		out = append(out, row)
	}
	return out, nil
}

func (h *Handler) renderReturnRefusal(w http.ResponseWriter, r *http.Request, err error) bool {
	refused, ok := errors.AsType[*FormRefusalError](err)
	if !ok {
		return false
	}
	queue, readErr := h.store.Queue(r.Context(), r.URL.Query().Get(web.KeysetParam))
	if readErr != nil {
		h.log.ErrorContext(r.Context(), "read return queue after refusal", "error", readErr)
		access.ServerError(w, r, h.log)
		return true
	}
	field := refused.Field
	if field == "" {
		field = "decision"
	}
	msg := refusalMessage(r, refused.Kind)
	if field == "basis" {
		msg = i18n.T(r.Context(), i18n.KeyAdminRetErrBasis)
	}
	view := admin.ReturnsView{
		Bound:  queue.Bound,
		Rows:   queue.Rows,
		Errors: map[string]string{r.PathValue("id") + "." + field: msg},
	}
	for i := range view.Rows {
		if view.Rows[i].ID != r.PathValue("id") {
			continue
		}
		if _, submitted := r.PostForm["basis"]; submitted {
			view.Rows[i].AssessmentBasis = r.PostFormValue("basis")
		}
		if resolution := r.PostFormValue("resolution"); resolution != "" {
			view.Rows[i].Resolution = resolution
		}
		overlayDraftFacts(&view.Rows[i], r)
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Returns(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageReturns)}, view))
	return true
}

func (h *Handler) renderInspection(w http.ResponseWriter, r *http.Request, key i18n.Key) {
	queue, readErr := h.store.Queue(r.Context(), r.URL.Query().Get(web.KeysetParam))
	if readErr != nil {
		h.log.ErrorContext(r.Context(), "read return queue after refused inspection", "error", readErr)
		access.ServerError(w, r, h.log)
		return
	}
	id := r.PathValue("id")
	view := admin.ReturnsView{
		Bound:  queue.Bound,
		Rows:   queue.Rows,
		Errors: map[string]string{id + ".inspect": i18n.T(r.Context(), key)},
	}
	for i := range view.Rows {
		if view.Rows[i].ID != id {
			continue
		}
		for j := range view.Rows[i].Lines {
			line := &view.Rows[i].Lines[j]
			line.DraftReceived = r.PostFormValue("received_" + line.OrderLineID)
			line.DraftRestocked = r.PostFormValue("restocked_" + line.OrderLineID)
			line.DraftNote = r.PostFormValue("note_" + line.OrderLineID)
		}
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Returns(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageReturns)}, view))
}

func overlayDraftFacts(row *admin.Return, r *http.Request) {
	for i := range row.Lines {
		id := row.Lines[i].OrderLineID
		if v := strings.TrimSpace(r.PostFormValue("unused_" + id)); v != "" {
			row.Lines[i].Unused = v
		}
		if v := strings.TrimSpace(r.PostFormValue("packaging_" + id)); v != "" {
			row.Lines[i].Packaging = v
		}
		if v := strings.TrimSpace(r.PostFormValue("accessories_" + id)); v != "" {
			row.Lines[i].Accessories = v
		}
	}
}

func refusalMessage(r *http.Request, kind returnrules.RefusalKind) string {
	key, ok := refusalKeys[kind]
	if !ok {
		return i18n.T(r.Context(), i18n.KeyAdminNoticeRefused)
	}
	return i18n.T(r.Context(), key)
}

var refusalKeys = map[returnrules.RefusalKind]i18n.Key{
	returnrules.RefuseStatutoryReject: i18n.KeyAdminRetErrStatutoryReject,
	returnrules.RefuseIncomplete:      i18n.KeyAdminRetErrIncomplete,
	returnrules.RefuseNeedException:   i18n.KeyAdminRetErrNeedException,
	returnrules.RefuseUnmetApprove:    i18n.KeyAdminRetErrUnmetApprove,
	returnrules.RefuseNoUnmet:         i18n.KeyAdminRetErrNoUnmet,
	returnrules.RefuseUseApprove:      i18n.KeyAdminRetErrUseApprove,
	returnrules.RefuseStale:           i18n.KeyAdminRetErrStale,
	returnrules.RefuseEmpty:           i18n.KeyAdminRetErrIncomplete,
	returnrules.RefuseExceptionReason: i18n.KeyAdminRetErrExceptionReason,
	returnrules.RefuseRejectionReason: i18n.KeyAdminRetErrRejectionReason,
}

func (h *Handler) Inspect(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}

	lines, parseErr := inspectionLines(r)
	if parseErr != nil {
		h.log.WarnContext(r.Context(), "return inspection rejected",
			"return", r.PathValue("id"), "error", parseErr)
		h.renderInspection(w, r, i18n.KeyAdminNoticeBadCount)
		return
	}

	err := h.store.Inspect(r.Context(), r.PathValue("id"), lines, audit.ActorID(r.Context()))
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/returns?inspected=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid):
		h.renderInspection(w, r, i18n.KeyAdminNoticeBadCount)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "return inspection refused",
			"return", r.PathValue("id"), "error", err)
		h.renderInspection(w, r, i18n.KeyAdminNoticeRefused)
	default:
		h.log.ErrorContext(r.Context(), "inspect return",
			"return", r.PathValue("id"), "error", err)
		access.ServerError(w, r, h.log)
	}
}

// inspectionLines reads the per-line counts off the form, keyed by line id for
// parcelLines' reason: two drifted lists would restock the wrong variant.
func inspectionLines(r *http.Request) ([]LineInspection, error) {
	var out []LineInspection
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
		out = append(out, LineInspection{
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

func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.Complete(r.Context(), r.PathValue("id"),
		r.PostFormValue("resolution"))
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
		access.ServerError(w, r, h.log)
	}
}

// confirmReturnDecision renders the selected operation before Decide can move
// money. The final POST still revalidates current eligibility and assessment.
func (h *Handler) confirmReturnDecision(w http.ResponseWriter, r *http.Request) bool {
	kind, ok := returnrules.ParseDecisionKind(r.PostFormValue("decision"))
	if !ok {
		return false
	}
	resolution := r.PostFormValue("resolution")
	required := kind == returnrules.DecisionReject || kind == returnrules.DecisionException
	confirmed := r.PostFormValue("confirm") == string(kind)
	if confirmed {
		return false
	}
	row, retry, err := h.store.returnUnderDecision(r.Context(), r.PathValue("id"), kind)
	if err != nil {
		if errors.Is(err, ErrRefused) {
			http.Redirect(w, r, "/admin/returns?refused=1", http.StatusSeeOther)
		} else {
			access.ServerError(w, r, h.log)
		}
		return true
	}
	view := admin.ReturnConfirmation{ID: row.ID.String(), OrderNumber: row.OrderNumber, Decision: string(kind), Reason: row.Reason, AmountCents: row.RefundableCents, Resolution: resolution, AssessmentVersion: r.PostFormValue("assessment_version"), Required: required, Retry: retry}
	web.Render(w, r, h.log, http.StatusOK, admin.ConfirmReturn(layouts.Page{Title: view.Title(r.Context())}, view))
	return true
}
