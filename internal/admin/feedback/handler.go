package feedback

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
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
		panic("feedback: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/messages", ac.RequireStaff(h.Messages))
	mux.HandleFunc("POST /admin/messages/handle", ac.RequireStaff(h.HandleMessage))
	mux.HandleFunc("POST /admin/messages/reopen", ac.RequireStaff(h.ReopenMessage))
	mux.HandleFunc("GET /admin/reviews", ac.RequireStaff(h.Reviews))
	mux.HandleFunc("POST /admin/reviews/hide", ac.RequireStaff(h.HideReview))
	mux.HandleFunc("POST /admin/reviews/show", ac.RequireStaff(h.ShowReview))
	mux.HandleFunc("GET /admin/questions", ac.RequireStaff(h.Questions))
	mux.HandleFunc("POST /admin/questions/{id}", ac.RequireStaff(h.AnswerQuestion))
}

var notices = map[string]web.NoticeEntry{
	"ok":      web.Done(i18n.KeyAdminNoticeOK),
	"refused": web.Refused(i18n.KeyAdminNoticeRefused),
	"gone":    web.Refused(i18n.KeyAdminNoticeGone),
}

func (h *Handler) Questions(w http.ResponseWriter, r *http.Request) {
	queue := VisibleQuestions
	if r.URL.Query().Get("hidden") == "1" {
		queue = HiddenQuestions
	}
	view, err := h.store.Questions(r.Context(), queue, r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read questions", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = web.Notice(r, notices)
	web.Render(w, r, h.log, http.StatusOK, admin.Questions(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageQuestions)}, view))
}

func (h *Handler) AnswerQuestion(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		access.NotFound(w, r, h.log)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")

	var err error
	switch admin.QuestionAction(r.PostFormValue("action")) {
	case admin.QuestionHideAction:
		err = h.store.HideQuestion(r.Context(), id)
	case admin.QuestionShowAction:
		err = h.store.ShowQuestion(r.Context(), id)
	case admin.QuestionHideAnswerAction:
		err = h.store.HideAnswer(r.Context(), id, r.PostFormValue("answer_id"))
	case admin.QuestionAnswerAction, "":
		err = h.store.AnswerQuestion(r.Context(), id, u.ID, r.PostFormValue("body"))
	default:
		err = ErrNotFound
	}

	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/questions?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid):
		h.rejectAnswer(w, r, id)
	case errors.Is(err, ErrNotFound):
		http.Redirect(w, r, "/admin/questions?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "answer question", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) rejectAnswer(w http.ResponseWriter, r *http.Request, id string) {
	view, err := h.store.Questions(r.Context(), VisibleQuestions)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read questions after refused answer", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	for i := range view.Rows {
		if view.Rows[i].ID == id {
			view.Rows[i].Draft = r.PostFormValue("body")
			view.Rows[i].Error = i18n.T(r.Context(), i18n.KeyAdminQuestionBodyError)
		}
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Questions(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageQuestions)}, view))
}

func (h *Handler) Reviews(w http.ResponseWriter, r *http.Request) {
	filter := AllReviews
	if r.URL.Query().Get("rating") == "3" {
		filter = ThreeStarsAndBelowReviews
	}
	view, err := h.store.Reviews(r.Context(), filter, r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read reviews", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = web.Notice(r, notices)
	web.Render(w, r, h.log, http.StatusOK, admin.Reviews(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageReviews)}, view))
}

func (h *Handler) HideReview(w http.ResponseWriter, r *http.Request) {
	h.setReviewHidden(w, r, true)
}

func (h *Handler) ShowReview(w http.ResponseWriter, r *http.Request) {
	h.setReviewHidden(w, r, false)
}

func (h *Handler) setReviewHidden(w http.ResponseWriter, r *http.Request, hidden bool) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	switch err := h.store.SetReviewHidden(r.Context(), r.PostFormValue("review"), hidden); {
	case err == nil:
		http.Redirect(w, r, "/admin/reviews?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		http.Redirect(w, r, "/admin/reviews?gone=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set review hidden", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) Messages(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Messages(r.Context(), r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read contact messages", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = web.Notice(r, notices)
	web.Render(w, r, h.log, http.StatusOK, admin.Messages(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageMessages)}, view))
}

func (h *Handler) HandleMessage(w http.ResponseWriter, r *http.Request) {
	h.setMessageHandled(w, r, true)
}

func (h *Handler) ReopenMessage(w http.ResponseWriter, r *http.Request) {
	h.setMessageHandled(w, r, false)
}

func (h *Handler) setMessageHandled(w http.ResponseWriter, r *http.Request, handled bool) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	switch err := h.store.SetMessageHandled(r.Context(), r.PostFormValue("message"), handled); {
	case err == nil:
		http.Redirect(w, r, "/admin/messages?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		http.Redirect(w, r, "/admin/messages?gone=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set message handled", "error", err)
		access.ServerError(w, r, h.log)
	}
}
