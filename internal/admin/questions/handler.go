package questions

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/frame"
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
		panic("questions: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *admin.Control) {
	mux.HandleFunc("GET /admin/questions", ac.RequireStaff(h.Questions))
	mux.HandleFunc("POST /admin/questions/{id}", ac.RequireStaff(h.AnswerQuestion))
}

var notices = map[string]widget.NoticeEntry{
	"ok":      widget.NoticeDone(i18n.KeyAdminNoticeOK),
	"refused": widget.NoticeRefused(i18n.KeyAdminNoticeRefused),
}

func (h *Handler) Questions(w http.ResponseWriter, r *http.Request) {
	queue := QueueVisible
	if r.URL.Query().Get("hidden") == "1" {
		queue = QueueHidden
	}
	view, err := h.store.Questions(r.Context(), queue, r.URL.Query().Get(keyset.Param))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read questions", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	view.Notice = widget.NoticeFrom(r, notices)
	respond.Component(w, r, h.log, http.StatusOK, Questions(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageQuestions)}, view))
}

func (h *Handler) AnswerQuestion(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		admin.NotFound(w, r, h.log)
		return
	}
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")

	var err error
	switch Action(r.PostFormValue("action")) {
	case ActionHide:
		err = h.store.HideQuestion(r.Context(), id)
	case ActionShow:
		err = h.store.ShowQuestion(r.Context(), id)
	case ActionHideAnswer:
		err = h.store.HideAnswer(r.Context(), id, r.PostFormValue("answer_id"))
	case ActionAnswer, "":
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
		admin.ServerError(w, r, h.log)
	}
}

func (h *Handler) rejectAnswer(w http.ResponseWriter, r *http.Request, id string) {
	view, err := h.store.Questions(r.Context(), QueueVisible)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read questions after refused answer", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	for i := range view.Rows {
		if view.Rows[i].ID == id {
			view.Rows[i].Draft = r.PostFormValue("body")
			view.Rows[i].Error = i18n.T(r.Context(), i18n.KeyAdminQuestionBodyError)
		}
	}
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, Questions(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageQuestions)}, view))
}
