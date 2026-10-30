package returns

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/order/returns"
	"github.com/koopa0/goen/internal/payment/refund"
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
		panic("returns: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *admin.Control) {
	mux.HandleFunc("GET /admin/returns", ac.RequireStaff(h.Queue))
	mux.HandleFunc("POST /admin/returns/{id}/decide", ac.RequireStaff(h.Decide))
	mux.HandleFunc("POST /admin/returns/{id}/assess", ac.RequireStaff(h.Assess))
	mux.HandleFunc("POST /admin/returns/{id}/inspect", ac.RequireStaff(h.Inspect))
	mux.HandleFunc("POST /admin/returns/{id}/complete", ac.RequireStaff(h.Complete))
}

var notices = map[string]widget.NoticeEntry{
	"approved":       widget.NoticeDone(i18n.KeyAdminNoticeReturnApproved),
	"exception":      widget.NoticeDone(i18n.KeyAdminNoticeReturnException),
	"declined":       widget.NoticeDone(i18n.KeyAdminNoticeReturnDeclined),
	"refused":        widget.NoticeRefused(i18n.KeyAdminNoticeRefused),
	"refundfailed":   widget.NoticeFailed(i18n.KeyAdminNoticeRefundFailed),
	"refundfollowup": widget.NoticeFailed(i18n.KeyAdminNoticeReturnFollowUp),
	"assessed":       widget.NoticeDone(i18n.KeyAdminNoticeAssessed),
	"inspected":      widget.NoticeDone(i18n.KeyAdminNoticeInspected),
	"closed":         widget.NoticeDone(i18n.KeyAdminNoticeClosed),
}

func (h *Handler) Queue(w http.ResponseWriter, r *http.Request) {
	var queue Queue
	var err error
	own := false
	if raw := r.URL.Query().Get("request"); raw != "" {
		request, parseErr := uuid.Parse(raw)
		if parseErr != nil || request == uuid.Nil {
			admin.NotFound(w, r, h.log)
			return
		}
		own = true
		queue, err = h.store.RequestQueue(r.Context(), request, r.URL.Query().Get(keyset.Param))
	} else {
		queue, err = h.store.Queue(r.Context(), r.URL.Query().Get(keyset.Param))
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "read return queue", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	// A page for one return that is not there is a link to nothing, not a queue
	// with nobody waiting in it.
	if own && len(queue.Rows) == 0 {
		admin.NotFound(w, r, h.log)
		return
	}
	for _, issue := range queue.payoutIssues {
		h.log.ErrorContext(r.Context(), "return payout no longer fits its sources",
			"return_id", issue.returnID, "error", issue.err)
	}
	view := View{
		Bound:   queue.Bound,
		Rows:    queue.Rows,
		OwnPage: own,
		Notice:  widget.NoticeFrom(r, notices),
	}
	respond.Component(w, r, h.log, http.StatusOK, Returns(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminQueueReturns)}, view))
}

// Decide approves or rejects a return. Approving pays money back, so a refusal
// from the database or from Stripe is reported and never swallowed.
func (h *Handler) Decide(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
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
		kind, _ := returns.ParseDecisionKind(r.PostFormValue("decision"))
		http.Redirect(w, r, answerAt(r, decidedNotice(kind)), http.StatusSeeOther)
	case errors.Is(err, refund.ErrFollowUpIncomplete):
		// The money is back, so the notice must not send staff to Stripe. Test
		// this before ErrIncomplete, which it wraps.
		h.log.ErrorContext(r.Context(), "decide return",
			"return", r.PathValue("id"), "error", err)
		http.Redirect(w, r, answerAt(r, "refundfollowup"), http.StatusSeeOther)
	case errors.Is(err, refund.ErrIncomplete):
		// A payout may preserve a database refusal as its cause, but once approval
		// committed the operator needs the recovery notice, not the generic
		// "decision refused" notice. Test this before ErrRefused.
		h.log.ErrorContext(r.Context(), "decide return",
			"return", r.PathValue("id"), "error", err)
		http.Redirect(w, r, answerAt(r, "refundfailed"), http.StatusSeeOther)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrRefused), errors.Is(err, refund.ErrRefused):
		h.log.WarnContext(r.Context(), "return decision refused",
			"return", r.PathValue("id"), "error", err)
		if h.renderReturnRefusal(w, r, err) {
			return
		}
		http.Redirect(w, r, answerAt(r, "refused"), http.StatusSeeOther)
	default:
		// Infrastructure errors which occurred before this became a durable payout
		// recovery land here.
		h.log.ErrorContext(r.Context(), "decide return",
			"return", r.PathValue("id"), "error", err)
		admin.ServerError(w, r, h.log)
	}
}

// Assess records eligibility facts and never pays or restocks.
func (h *Handler) Assess(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	facts, parseErr := assessmentLines(r)
	if parseErr != nil {
		h.log.WarnContext(r.Context(), "return assessment rejected",
			"return", r.PathValue("id"), "error", parseErr)
		if h.renderReturnRefusal(w, r, formRefuse("decision", returns.RefuseIncomplete)) {
			return
		}
		http.Redirect(w, r, answerAt(r, "refused"), http.StatusSeeOther)
		return
	}
	err := h.store.Assess(r.Context(), r.PathValue("id"), r.PostFormValue("basis"), facts)
	switch {
	case err == nil:
		http.Redirect(w, r, answerAt(r, "assessed"), http.StatusSeeOther)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "return assessment refused",
			"return", r.PathValue("id"), "error", err)
		if h.renderReturnRefusal(w, r, err) {
			return
		}
		http.Redirect(w, r, answerAt(r, "refused"), http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "assess return",
			"return", r.PathValue("id"), "error", err)
		admin.ServerError(w, r, h.log)
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
		fact, ok := returns.ParseFact(strings.TrimSpace(values[0]))
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
			row.Unused = string(returns.FactUnknown)
		}
		if row.Packaging == "" {
			row.Packaging = string(returns.FactUnknown)
		}
		if row.Accessories == "" {
			row.Accessories = string(returns.FactUnknown)
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
	queue, readErr := h.refusedQueue(r)
	if readErr != nil {
		h.log.ErrorContext(r.Context(), "read return queue after refusal", "error", readErr)
		admin.ServerError(w, r, h.log)
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
	_, own := ownPage(r)
	view := View{
		Bound:   queue.Bound,
		Rows:    queue.Rows,
		OwnPage: own,
		Errors:  map[string]string{r.PathValue("id") + "." + field: msg},
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
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, Returns(
		frame.Page{Refused: true, Title: i18n.T(r.Context(), i18n.KeyAdminQueueReturns)}, view))
	return true
}

func (h *Handler) renderInspection(w http.ResponseWriter, r *http.Request, key i18n.Key) {
	queue, readErr := h.refusedQueue(r)
	if readErr != nil {
		h.log.ErrorContext(r.Context(), "read return queue after refused inspection", "error", readErr)
		admin.ServerError(w, r, h.log)
		return
	}
	id := r.PathValue("id")
	_, own := ownPage(r)
	view := View{
		Bound:   queue.Bound,
		Rows:    queue.Rows,
		OwnPage: own,
		Errors:  map[string]string{id + ".inspect": i18n.T(r.Context(), key)},
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
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, Returns(
		frame.Page{Refused: true, Title: i18n.T(r.Context(), i18n.KeyAdminQueueReturns)}, view))
}

func overlayDraftFacts(row *Return, r *http.Request) {
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

func refusalMessage(r *http.Request, kind returns.RefusalKind) string {
	key, ok := refusalKeys[kind]
	if !ok {
		return i18n.T(r.Context(), i18n.KeyAdminNoticeRefused)
	}
	return i18n.T(r.Context(), key)
}

var refusalKeys = map[returns.RefusalKind]i18n.Key{
	returns.RefuseStatutoryReject:   i18n.KeyAdminRetErrStatutoryReject,
	returns.RefuseUndeliveredReject: i18n.KeyAdminRetErrUndeliveredReject,
	returns.RefuseIncomplete:        i18n.KeyAdminRetErrIncomplete,
	returns.RefuseNeedException:     i18n.KeyAdminRetErrNeedException,
	returns.RefuseUnmetApprove:      i18n.KeyAdminRetErrUnmetApprove,
	returns.RefuseNoUnmet:           i18n.KeyAdminRetErrNoUnmet,
	returns.RefuseUseApprove:        i18n.KeyAdminRetErrUseApprove,
	returns.RefuseStale:             i18n.KeyAdminRetErrStale,
	returns.RefuseEmpty:             i18n.KeyAdminRetErrIncomplete,
	returns.RefuseExceptionReason:   i18n.KeyAdminRetErrExceptionReason,
	returns.RefuseRejectionReason:   i18n.KeyAdminRetErrRejectionReason,
}

func (h *Handler) Inspect(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
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
		http.Redirect(w, r, answerAt(r, "inspected"), http.StatusSeeOther)
	case errors.Is(err, ErrInvalid):
		h.renderInspection(w, r, i18n.KeyAdminNoticeBadCount)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "return inspection refused",
			"return", r.PathValue("id"), "error", err)
		h.renderInspection(w, r, i18n.KeyAdminNoticeRefused)
	default:
		h.log.ErrorContext(r.Context(), "inspect return",
			"return", r.PathValue("id"), "error", err)
		admin.ServerError(w, r, h.log)
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
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.Complete(r.Context(), r.PathValue("id"),
		r.PostFormValue("resolution"))
	switch {
	case err == nil:
		http.Redirect(w, r, answerAt(r, "closed"), http.StatusSeeOther)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "return completion refused",
			"return", r.PathValue("id"), "error", err)
		http.Redirect(w, r, answerAt(r, "refused"), http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "complete return",
			"return", r.PathValue("id"), "error", err)
		admin.ServerError(w, r, h.log)
	}
}

// confirmReturnDecision renders the selected operation before Decide can move
// money. The final POST still revalidates current eligibility and assessment.
func (h *Handler) confirmReturnDecision(w http.ResponseWriter, r *http.Request) bool {
	kind, ok := returns.ParseDecisionKind(r.PostFormValue("decision"))
	if !ok {
		return false
	}
	resolution := r.PostFormValue("resolution")
	required := kind == returns.DecisionReject || kind == returns.DecisionException
	confirmed := r.PostFormValue("confirm") == string(kind)
	if confirmed {
		return false
	}
	row, retry, err := h.store.returnUnderDecision(r.Context(), r.PathValue("id"), kind)
	if err != nil {
		if errors.Is(err, ErrRefused) {
			http.Redirect(w, r, answerAt(r, "refused"), http.StatusSeeOther)
		} else {
			admin.ServerError(w, r, h.log)
		}
		return true
	}
	_, own := ownPage(r)
	view := Confirmation{ID: row.ID.String(), OrderNumber: row.OrderNumber, Decision: string(kind), Reason: row.Reason, AmountCents: row.RefundableCents, Resolution: resolution, AssessmentVersion: r.PostFormValue("assessment_version"), Required: required, Retry: retry, OwnPage: own}
	respond.Component(w, r, h.log, http.StatusOK, ConfirmReturn(frame.Page{Title: view.Title(r.Context())}, view))
	return true
}

// decidedNotice is what a decision that took says. Approval says only that the
// return was approved: what was paid back is the return card's own status,
// which follows the verified payout.
func decidedNotice(kind returns.DecisionKind) string {
	switch kind {
	case returns.DecisionApprove:
		return "approved"
	case returns.DecisionException:
		return "exception"
	case returns.DecisionReject:
		return "declined"
	default:
		// Decide refused any other kind before it wrote.
		return ""
	}
}

const (
	fromField   = "from"
	fromOwnPage = "return"
)

// ownPage is the return whose own page a form was posted from. The address of
// the answer is built from the path id and never read off the form, so a posted
// value cannot send staff anywhere else.
func ownPage(r *http.Request) (uuid.UUID, bool) {
	if r.PostFormValue(fromField) != fromOwnPage {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	return id, err == nil && id != uuid.Nil
}

// answerAt is where a form's answer goes: the queue, or the return's own page
// when the form was posted from it, so staff stay on the return they acted on.
// notice is the query parameter the page reads, or empty.
func answerAt(r *http.Request, notice string) string {
	var pairs []string
	if id, ok := ownPage(r); ok {
		pairs = append(pairs, "request", id.String())
	}
	if notice != "" {
		pairs = append(pairs, notice, "1")
	}
	return keyset.ScopeURL("/admin/returns", pairs...)
}

// refusedQueue reads what a refused form draws again: the return's own page
// when the form came from it, else the queue page the action URL names. Without
// this a return further down the queue loses its error and its drafts to the
// first page.
func (h *Handler) refusedQueue(r *http.Request) (Queue, error) {
	if id, ok := ownPage(r); ok {
		return h.store.RequestQueue(r.Context(), id, "")
	}
	return h.store.Queue(r.Context(), r.URL.Query().Get(keyset.Param))
}
